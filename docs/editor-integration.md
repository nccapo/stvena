# Editor integration

Stvena's VS Code extension follows reported reads, consecutive captured workspace
versions, and the source range currently selected in TUI review. It opens the
ordinary working source with `showTextDocument`, selecting the relevant line
and preserving keyboard focus during automatic updates, as described in the
[VS Code API](https://code.visualstudio.com/api/references/vscode-api#window.showTextDocument).
Deleted files are not opened; automatic following skips unsaved buffers.
Install instructions and product limits are in
[extensions/vscode/README.md](../extensions/vscode/README.md).

## Discovery

A consumer resolves a workspace folder to the **descriptor directory**, which
holds `stvena-live.json`, `stvena-review.json`, `stvena-request.json` and
`stvena-ide.json`. Resolution has two steps, in this order.

**1. Ask Git.** Run `git rev-parse --show-toplevel`, then resolve that root's
Git directory with `git rev-parse --absolute-git-dir`. On success that directory
is the descriptor directory, and resolution stops. Do not assume `.git` is a
directory: linked worktrees use their own descriptors. This step is unchanged
from the first version of this protocol, so a repository behaves exactly as it
always has.

**2. Ask Stvena's bridge registry.** A project that is not a Git repository is
still reviewed: Stvena captures it into a private store and writes its
descriptors beside that store, outside the user's source tree. It records where,
in one JSON file per reviewed project, in `$STVENA_HOME/bridges/`
(`~/.stvena/bridges/` unless `STVENA_HOME` is set). Read every `*.json` there:

| Field | Meaning |
| --- | --- |
| `version` | Entry version, currently `1` |
| `root` | Absolute worktree root Stvena is reviewing |
| `realRoot` | `root` with symlinks resolved; an editor may hand over either spelling |
| `mode` | `git` or `shadow` — whether the user's own repository backs this project |
| `dir` | **The descriptor directory**, in both modes |
| `gitDir` | Object store for `git cat-file blob`: the repository's Git directory, or Stvena's private one |
| `updatedAt` | RFC 3339 UTC, refreshed while Stvena runs |

Discard an entry unless `version` is 1, `mode` is one of the two names above,
every path is absolute and NUL-free, and `updatedAt` parses. Then discard any
entry whose `root` or `realRoot` is not the folder being resolved or one of its
ancestors — that rule is what stops a stray file pointing an editor at an
unrelated tree. Of the entries that remain, **the one with the longest
`realRoot` wins**, which resolves both "this folder is a subdirectory of the
reviewed root" and "one reviewed project sits inside another".

A folder with no Git answer and no matching entry is simply not being reviewed.
That is an ordinary state, not an error.

Entries are written in both modes, so there is one fallback path rather than one
that only runs where nobody tests it. They are not deleted when Stvena exits:
the registry is a map, not a liveness signal, and liveness is the `active` flag
and heartbeat below. An entry is a map of paths and nothing else — read `dir`
and `gitDir` as locations, and never derive a command from anything in it.

## Local bridge, version 1

Read `stvena-live.json` in the resolved descriptor directory. The writer replaces
the file atomically with owner-only permissions; it does not add files to the
user's source tree.

The JSON contains:

| Field | Meaning |
| --- | --- |
| `version` | Protocol version, currently `1` |
| `session` | Stvena session ID |
| `sequence` | Advances when the captured tree changes; starts at zero |
| `active` | False after normal watcher shutdown |
| `updatedAt` | RFC 3339 UTC heartbeat; treat older than 30 seconds as disconnected |
| `startedAt` | Optional RFC 3339 UTC time this Stvena process connected; see [one owner per project](#one-owner-per-project) |
| `error` | Optional capture error; don't follow stale edits while present |
| `changedAt` | Optional timestamp of the latest changed capture, unchanged by heartbeats |
| `activity` | Optional latest reported read, expires after 15 seconds |
| `files` | All changes in the latest changed capture, kept across idle heartbeats |

Each file has `path`, optional rename `oldPath`, Git `status`, immutable blob
object IDs `before` and `after`, one-based first changed `line`, `added` and
`deleted` counts, and `binary` / `truncated` flags. Optional `ranges` contains one-based inclusive
`{start, end}` changed spans in the new file; deletions point to a surviving
neighbor, clamped at EOF. Older producers without ranges still navigate by `line`. An all-zero object ID means
that side is absent. Read nonzero IDs with `git cat-file blob <oid>`; do not
execute source text or interpret it as a command. Submodule objects are not text
blobs and may fail text preview. Consumers must validate fields and paths.

The first comparison starts at the saved session baseline. Later comparisons
start at the last successfully published capture. Heartbeats do not advance the
sequence. The bridge publishes even when the terminal review is pinned. A
capture error preserves the last successful batch and reports an error until a
successful capture. Descriptor-write failures also appear in the terminal.

This descriptor is a latest-state protocol, without acknowledgements, replay,
per-agent attribution, or an ordered history within each batch. The retained TUI
session timeline is separate and is not exposed as patch data here. Source
retention follows
Stvena's existing snapshot refs: do not treat old object IDs as a durable archive.
One Stvena process per repository remains the supported model.

## Read activity and visual markers

`activity` carries `session`, unique `id`, `agent` command name, repository-relative
`path`, one-based `line`, optional inclusive `endLine`, and RFC 3339 `updatedAt`.
An omitted `endLine` means the range is unknown. Consumers validate paths,
integers, timestamps, and session identity. Reads do not advance capture
`sequence`; consumers independently track the activity ID. Read expiry never
navigates back to an old edit. The newest reported event wins automatic follow.

Standard Codex and Claude launches receive invocation-local PostToolUse hooks
calling the same executable's `editor-hook` command. The child receives
`STVENA_ACTIVITY_PATH`, `STVENA_ROOT`, `STVENA_SESSION`, and `STVENA_AGENT`.
The observer parses supported inputs without executing command text, confines
paths to the real repository root, and atomically writes location-only metadata
to `<session-id>-activity.json` in Stvena's private cache. The snapshot publisher
includes only fresh activity belonging to its session. Observer failures never
approve, deny, or alter a tool call. Existing user settings are not written.
See the [extension guide](../extensions/vscode/README.md#following-reads) for
supported commands, hook trust, custom settings, and coverage limits.

Read and edit ranges use separate whole-line decorations, gutter icons, inline
labels, and overview-ruler markers. They leave text and terminal focus intact.
Markers clear after 15 seconds, on pause/disconnect, or while buffers are dirty.
Saved edits retain workspace-wide attribution; an edit marker does not claim
that a specific agent made the change. Only the latest followed location is marked.

## Review bridge and editor requests

### One owner per project

Two Stvena processes can run in the same project. Both descriptors are owned by
one of them at a time, or the editor would show each one's state in turn.
Before writing, Stvena reads the descriptor and steps back while it belongs to
another session that is `active`, was updated within 15 seconds, and has a
later `startedAt`. The newest process wins. The older one says so in its
notice, and resumes when the newer one exits (it writes `active: false`) or
stops writing for 15 seconds. A descriptor without `startedAt` counts as older.
Editors need do nothing: requests carry the owner's `session`, and only that
Stvena acts on them.

Review state uses a separate source-only `stvena-review.json` descriptor in the
resolved descriptor directory. It contains no patch or source text; Stvena remains the
authoritative diff surface. The version 1 fields are:

| Field | Meaning |
| --- | --- |
| `version`, `session`, `sequence`, `active`, `updatedAt`, `changedAt` | Versioned session identity, change counter, lifecycle, heartbeat, and last focus/count change |
| `startedAt` | Optional time this Stvena process connected; see [one owner per project](#one-owner-per-project) |
| `focus` | Optional captured `tree`, review `source`, repository-relative `path`, and inclusive one-based `line` / `endLine` |
| `unreviewedFiles`, `unreviewedHunks` | Remaining review work in the live cumulative session view |
| `newerBatches` | Observed timeline batches newer than the currently pinned batch |
| `features` | Request actions this Stvena accepts; absent means only `review` and `context` |
| `tree` | Captured tree the `files` below describe |
| `files` | Per-file review state, each with `path`, optional `oldPath`, `status`, `reviewed`, `rejected`, `binary`, optional `before` blob, and `hunks` |
| `truncated` | Set when the change set exceeded 500 files or 5000 hunks |
| `pendingRejections` | Optional `count`, `appliesAt` (`now`, `turn-end` or `manual`) and `reason` |
| `lastRequest` | Optional acknowledgement: `id`, `action`, `status` (`applied`, `queued`, `refused`), `message`, `at` |

Each hunk has an opaque 64-character hex `id` and inclusive one-based `start` /
`end` lines **in the ordinary working file**, not offsets into a patch. A
deletion-only hunk points at the surviving neighbour. The id is derived from the
hunk's content, so a hunk the agent edited after the editor drew it produces a
different id and any request naming the old one is refused. Consumers must treat
the id as opaque and echo it back unchanged.

An undecided hunk can also say what it replaced. `removed` lists the lines it
took out as `[start, count]` runs, numbered in the file's `before` blob, and
`added` counts the lines it put in. A hunk with `removed` and no `added` only
deleted lines. No source text enters the descriptor: a consumer that wants to
show the removed lines reads `before` from the object store, exactly like the
blobs in live edit state. Decided hunks, and files with no before version (new
files), carry none of these fields.

**Every field from `features` onward is optional.** An older Stvena omits them
and a consumer must treat absence as "this build cannot do that", never as a
protocol error. Gate UI on `features` rather than on a version number.

The extension renders the current focus as a persistent purple source marker and
opens only the working file. Consumers must validate the tree object ID, path,
ranges, counts, timestamps, and heartbeat exactly as they do for live edit state.
The captured tree records provenance; the ordinary working file may have moved on.

User-initiated editor actions atomically replace `stvena-request.json` with
owner-only permissions. A version 1 request contains `version`, matching
`session`, unique `id`, an `action` the descriptor's `features` advertises,
validated repository `path`, inclusive one-based `line` / `endLine`, and
`updatedAt`. It may also carry `hunkId` (a hunk id from the descriptor; absent
means the whole file) and `text` (at most 4096 bytes, used as a rejection
reason). `apply-rejections`, `next-unreviewed` and `accept-all` act on the
whole queue and carry no location. `accept-all` also carries `tree`: the
descriptor's `tree` the editor counted from. Stvena refuses it if its session
tree has moved on since, so it never accepts a change the editor did not show.

Stvena reads the request file once per capture refresh, roughly every 700 ms,
and each request replaces the last. **Write one request at a time**: wait until
`lastRequest.id` names the one you wrote before writing the next, with a
timeout, since not every action is acknowledged. Two requests written between
two reads lose the first without any error.

| Action | Effect |
| --- | --- |
| `review` | Navigates the TUI without opening an IDE diff |
| `context` | Loads the range from the immutable capture into the context tray |
| `accept` | Marks the file or hunk reviewed — the same state the Space and H keys write |
| `reject` | Queues the file or hunk for reverting; **never writes to the working tree here** |
| `undo-reject` | Removes a queued rejection |
| `apply-rejections` | Applies the queue now, overriding the turn-boundary wait |
| `next-unreviewed` | Moves the TUI to the next unreviewed file |
| `accept-all` | Marks every undecided file and hunk reviewed, leaving queued rejections alone |
| `prompt` | Places `text` and the captured range in the agent's input, unsubmitted |

`prompt` reads the range from Stvena's immutable capture, never from editor
text, and the draft is pasted rather than submitted: the user reads it and
presses Enter.

## Projects without a Git repository

Everything in version 1 is identical in a project Stvena snapshots privately.
The differences a consumer can see are these:

- The registry entry's `mode` is `shadow`, and `dir` is Stvena's per-project
  cache directory rather than a Git directory.
- Blobs are read from `gitDir`, which is Stvena's private object store:
  `git --git-dir <gitDir> cat-file blob <oid>`. Object IDs are still opaque and
  may be 40 or 64 hex characters.
- `focus.source` never reports `branch`, because branch comparison needs a
  repository. A consumer keeps accepting the value: the descriptor shape does
  not change, and the same consumer may be talking to a Git-backed project a
  moment later.
- The TUI's workspace view lists everything that changed since the version
  Stvena first captured, with no staged/unstaged distinction, and staging is
  unavailable. Neither is visible in these descriptors.

Stvena writes nothing into the reviewed folder in either mode.

## Language server consumer

`stvena editor-lsp [--ide NAME]`, added in 0.4.0-preview.2, is a second consumer
of everything above, speaking standard LSP on stdin/stdout (`internal/editorlsp`). It exists because
Zed's extension API cannot draw markers, register commands, or open files, and
because any LSP-capable editor can then launch one binary instead of needing its
own extension. The Zed wrapper that launches it lives in
[nccapo/zed-stvena](https://github.com/nccapo/zed-stvena); `--ide` only sets the
name written to `stvena-ide.json`.

It resolves a folder the same way every consumer must: Git first, then
Stvena's bridge registry, so a folder reviewed without being a repository is
found too. It applies the same validation, the same 30-second heartbeat, the same
15-second activity expiry, the same `features` gating, and the same
last-request-wins request channel as the VS Code extension, and it writes only
`stvena-request.json` and `stvena-ide.json`. The two consumers are cross-checked
by their own protocol tests against the same descriptor shapes.

| Surface | LSP mechanism |
| --- | --- |
| Follow navigation | `window/showDocument` with `takeFocus: false` and a selection, sent only to an editor that advertises `window.showDocument.support` |
| Accept / reject a block | `textDocument/codeLens` plus `workspace/executeCommand` |
| Selection actions | `textDocument/codeAction`, kind `source.stvena` |
| Read / edit / review markers | `textDocument/inlayHint` |
| Review queue | `textDocument/publishDiagnostics`, Information per unreviewed hunk |
| Status | one long-lived `$/progress` token |

Two deliberate differences from the VS Code extension, both forced by LSP:

- **Whole-document text sync.** The server declares sync kind 1 rather than
  incremental, because it decides whether a buffer is unsaved by comparing it
  against the file on disk. That comparison is what keeps a file the agent wrote
  — which the editor reloads and reports as a change with no save — followable
  instead of permanently unsaved. An incremental change the editor sends anyway
  is treated as unsaved until the next save.
- **Follow navigation is optional.** Opening a file is a request the editor has
  to advertise, and Zed 1.19 does not implement `window/showDocument`. The
  server checks the client capability, explains once through `window/logMessage`
  that locations will be marked rather than opened, and keeps every other
  surface accurate. Editors that support it navigate as the VS Code extension
  does.
- **No reject-with-reason.** LSP has no text prompt, so the editor sends the
  rejection and the reason is added in the TUI. `prompt` likewise sends the
  range with empty text and tells the user to finish the question in the agent's
  input.

## Editor presence

An extension announces itself by atomically replacing `stvena-ide.json` in the
resolved descriptor directory with `version` 1, an `ide` name, its `extension` version,
and an RFC 3339 `updatedAt` it refreshes at least every 30 seconds. The language
server writes `ide: "zed"` when Zed's extension launches it, or the name given
to `--ide`, and removes its own descriptor on shutdown so the terminal leaves
IDE mode at once rather than waiting for the heartbeat to age out. Stvena
offers IDE mode only while that heartbeat is current: several editors report
`TERM_PROGRAM=vscode` and the extension may not be installed in the one running
Stvena, so the environment alone is not evidence of a connection. A missing,
malformed or stale descriptor simply means no editor is watching. The `ide` name
reaches the terminal UI, so Stvena strips control characters and truncates it.

A rejection is queued, not applied. Stvena reverts it only when every live agent
is between turns, because reverting under a working agent makes it re-apply the
change. `pendingRejections.appliesAt` says whether the queue is about to run
(`now`), is waiting for a turn to end (`turn-end`), or will only ever run when
the user asks (`manual`, for an agent that reports no turn boundaries). An
editor should show that wait rather than appearing stuck. Stvena accepts a
request once, only for its current session, and only within a one-minute freshness
window. `review` navigates the TUI without opening an IDE diff. `context` loads
the range from Stvena's immutable latest project capture rather than trusting
editor text, then saves it in the context tray. Dirty editor buffers are rejected
by the extension before either request is written.

This remains a local, last-request-wins control channel. `lastRequest` reports
what Stvena did with the request it most recently accepted, so an extension can
show a real outcome; until a matching `id` appears there, an extension should
say that it sent a request rather than claim Stvena completed it. Because the
channel is last-request-wins, a request written before the previous one is
acknowledged can replace it.

## Verification

Run `go test -race ./...`, `go vet ./...`, `go build ./...`, and `npm test` from
`extensions/vscode`. The language server's own suite is
`go test ./internal/editorlsp/`: it drives a real `Run` over pipes with a
scripted LSP client — initialize, didOpen, fixture descriptors, lens contents,
diagnostics, executeCommand, request-file assertions, refusal rollback — and
prints `STVENA_LSP_TESTS_PASSED` when it passes. Check that stdout carries
nothing but frames with `stvena editor-lsp < /dev/null | xxd | head`. To
exercise an actual editor host, use a disposable project and an isolated editor
user-data/extensions directory, then launch:

```sh
STVENA_HOME=/tmp/stvena-editor-home code /tmp/stvena-editor-project \
  --user-data-dir /tmp/stvena-editor-profile \
  --extensions-dir /tmp/stvena-editor-extensions \
  --disable-workspace-trust \
  --extensionDevelopmentPath=/absolute/path/to/stvena/extensions/vscode \
  --extensionTestsPath=/absolute/path/to/stvena/extensions/vscode/test/host.js
```

`STVENA_HOME` is required, and must be disposable: the suite writes a bridge
entry there, and it is removed afterwards along with the rest of that directory.

The suite runs the whole scenario **twice** — once against a Git repository it
creates in the workspace, and once with no repository at all, resolved through a
bridge entry pointing at a descriptor directory outside the workspace. Each pass
uses its own session and its own fixture filenames, because an editor keeps a
document for a file it has opened after the editor closes, and a stale buffer
would otherwise answer an assertion about what the current pass published. A
failure names the pass, so a failure that only happens without Git is not
triaged as one of the known Git-mode flakes. `STVENA_HOST_MODE=git` or
`=shadow` runs a single pass. Nothing in the scenario calls Git: the extension
never dereferences a captured object ID, so the fixture holds its own contents
and hands out synthetic ones.

The host smoke test writes only to that disposable workspace and that disposable
`STVENA_HOME`. It checks automatic
source opening and line selection, reads without saved changes, TUI review focus,
editor request descriptors, read expiry, pause/resume, addition/deletion handling,
unsaved buffer preservation, and the absence of diff tabs. It also covers the
accept/reject surface: the actions above a change block, a decision appearing
before Stvena confirms it, a refused decision rolling back, applying a queued
rejection, actions disappearing on an unsaved buffer, and a request being
withheld when the running Stvena does not advertise that action.

On macOS the `code` wrapper detaches and returns before the tests finish. Run
`/Applications/Visual Studio Code.app/Contents/MacOS/Code` with the same
arguments to see the result and the exit status. Use the editor's equivalent CLI to validate a VS Code
fork. Passing the protocol tests alone does not establish editor compatibility.

On 2026-09-15, the two-pass suite — Git repository and project without one —
passed in stock VS Code 1.137.0 and in Antigravity IDE 2.5.5 (VS Code base
1.107.0), both on Apple Silicon, against a source build of the extension. The Go
race suite, vet and build checks, and the extension unit tests also passed.

On 2026-09-10, the original saved-edit smoke test passed in stock VS Code
1.137.0 (Apple Silicon) and Antigravity IDE (VS Code base 1.107.0) on macOS.
The expanded read/range/pause/expiry smoke test passed in Antigravity. Its
activity inputs are protocol fixtures; live model-driven hook execution remains
unverified. Other forks and remote editor hosts remain unverified.

On 2026-09-14, the expanded suite including the accept/reject surface passed in
stock VS Code 1.137.0 and in Antigravity IDE 2.5.5 (VS Code base 1.107.0), both
on Apple Silicon, against a source build of the extension.

Check for the `STVENA_HOST_TESTS_PASSED` line, not the exit status. Several
editor launchers detach and return zero before the tests have run, so a silent
exit 0 means the suite never reported, not that it passed. Note also that
`Antigravity IDE.app` is the editor; the separate `Antigravity.app` is a
different application and blocks on its own auto-update.

For v0.2.0-preview.1, the packaged Stvena Live 0.2.0 VSIX was extracted and tested
in fresh, isolated VS Code and Antigravity profiles on macOS. Both passed the
expanded host suite: edits, reads, ranges, pause/resume, expiry, unsaved-buffer
preservation, and no diff tabs. The Go race suite, vet/build checks, and extension
unit tests also passed locally. One initial clipboard-fixture test timed out;
the full race suite passed on rerun.

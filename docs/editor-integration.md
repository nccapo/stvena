# Editor integration

Stvena's VS Code extension follows reported reads, consecutive captured workspace
versions, and the source range currently selected in TUI review. It opens the
ordinary working source with `showTextDocument`, selecting the relevant line
and preserving keyboard focus during automatic updates, as described in the
[VS Code API](https://code.visualstudio.com/api/references/vscode-api#window.showTextDocument).
Deleted files are not opened; automatic following skips unsaved buffers.
Install instructions and product limits are in
[extensions/vscode/README.md](../extensions/vscode/README.md).

## Local bridge, version 1

Resolve a workspace folder with `git rev-parse --show-toplevel`, then resolve that
root's Git directory with `git rev-parse --absolute-git-dir`. Read
`stvena-live.json` in that directory. Do not assume `.git` is a directory: linked
worktrees use their own descriptors. The writer replaces the file atomically
with owner-only permissions; it does not add files to the user's source tree.

The JSON contains:

| Field | Meaning |
| --- | --- |
| `version` | Protocol version, currently `1` |
| `session` | Stvena session ID |
| `sequence` | Advances when the captured tree changes; starts at zero |
| `active` | False after normal watcher shutdown |
| `updatedAt` | RFC 3339 UTC heartbeat; treat older than 30 seconds as disconnected |
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

Review state uses a separate source-only `stvena-review.json` descriptor in the
resolved Git directory. It contains no patch or source text; Stvena remains the
authoritative diff surface. The version 1 fields are:

| Field | Meaning |
| --- | --- |
| `version`, `session`, `sequence`, `active`, `updatedAt`, `changedAt` | Versioned session identity, change counter, lifecycle, heartbeat, and last focus/count change |
| `focus` | Optional captured `tree`, review `source`, repository-relative `path`, and inclusive one-based `line` / `endLine` |
| `unreviewedFiles`, `unreviewedHunks` | Remaining review work in the live cumulative session view |
| `newerBatches` | Observed timeline batches newer than the currently pinned batch |
| `features` | Request actions this Stvena accepts; absent means only `review` and `context` |
| `tree` | Captured tree the `files` below describe |
| `files` | Per-file review state, each with `path`, optional `oldPath`, `status`, `reviewed`, `rejected`, `binary`, and `hunks` |
| `truncated` | Set when the change set exceeded 500 files or 5000 hunks |
| `pendingRejections` | Optional `count`, `appliesAt` (`now`, `turn-end` or `manual`) and `reason` |
| `lastRequest` | Optional acknowledgement: `id`, `action`, `status` (`applied`, `queued`, `refused`), `message`, `at` |

Each hunk has an opaque 64-character hex `id` and inclusive one-based `start` /
`end` lines **in the ordinary working file**, not offsets into a patch. A
deletion-only hunk points at the surviving neighbour. The id is derived from the
hunk's content, so a hunk the agent edited after the editor drew it produces a
different id and any request naming the old one is refused. Consumers must treat
the id as opaque and echo it back unchanged.

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
reason). `apply-rejections` and `next-unreviewed` act on the whole queue and
carry no location.

| Action | Effect |
| --- | --- |
| `review` | Navigates the TUI without opening an IDE diff |
| `context` | Loads the range from the immutable capture into the context tray |
| `accept` | Marks the file or hunk reviewed — the same state the Space and H keys write |
| `reject` | Queues the file or hunk for reverting; **never writes to the working tree here** |
| `undo-reject` | Removes a queued rejection |
| `apply-rejections` | Applies the queue now, overriding the turn-boundary wait |
| `next-unreviewed` | Moves the TUI to the next unreviewed file |

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
`extensions/vscode`. To exercise an actual editor host, use a disposable Git
project and an isolated editor user-data/extensions directory, then launch:

```sh
code /tmp/stvena-editor-project \
  --user-data-dir /tmp/stvena-editor-profile \
  --extensions-dir /tmp/stvena-editor-extensions \
  --disable-workspace-trust \
  --extensionDevelopmentPath=/absolute/path/to/stvena/extensions/vscode \
  --extensionTestsPath=/absolute/path/to/stvena/extensions/vscode/test/host.js
```

The host smoke test writes only to that disposable workspace. It checks automatic
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

On 2026-09-10, the original saved-edit smoke test passed in stock VS Code
1.137.0 (Apple Silicon) and Antigravity IDE (VS Code base 1.107.0) on macOS.
The expanded read/range/pause/expiry smoke test passed in Antigravity. Its
activity inputs are protocol fixtures; live model-driven hook execution remains
unverified. Other forks and remote editor hosts remain unverified.

On 2026-09-14, the expanded suite including the accept/reject surface passed in
stock VS Code 1.137.0 (Apple Silicon) against a source build of the extension.
Antigravity was not re-verified for 0.3.0: its host blocked on an application
auto-update before the tests ran.

For v0.2.0-preview.1, the packaged Stvena Live 0.2.0 VSIX was extracted and tested
in fresh, isolated VS Code and Antigravity profiles on macOS. Both passed the
expanded host suite: edits, reads, ranges, pause/resume, expiry, unsaved-buffer
preservation, and no diff tabs. The Go race suite, vet/build checks, and extension
unit tests also passed locally. One initial clipboard-fixture test timed out;
the full race suite passed on rerun.

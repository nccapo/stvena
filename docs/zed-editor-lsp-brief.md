# Build brief: `stvena editor-lsp` and the Zed extension

For a coding agent working in `github.com/nccapo/stvena`. Read
`docs/zed-integration.md` (design and verified Zed facts) and
`docs/editor-integration.md` (bridge protocol) before starting. The Zed
extension scaffold is in the sibling repo `zed-stvena/`; it is finished except
where marked `AGENT:` and should not need more than a language-name review.

## Goal

Add a language server to the stvena binary that gives Zed the same surface the
VS Code extension gives VS Code, using only standard LSP. Zed launches it as
`stvena editor-lsp --ide zed` via the extension. The server is editor-agnostic;
`--ide` only sets the name written to `stvena-ide.json`.

Ship when the acceptance checklist at the bottom passes.

## Non-negotiable constraints

These are inherited from `docs/editor-integration.md` and the VS Code extension;
the two consumers must behave identically against the same descriptors.

1. Never open a diff tab; only the ordinary working file.
2. Never write source files. The only files the server writes are
   `stvena-request.json` and `stvena-ide.json` in the resolved descriptor
   directory, atomically, owner-only permissions.
3. Never execute descriptor content. Validate every field, path (confined to
   repo root, no symlink escape), integer, timestamp, and session identity
   exactly as `extensions/vscode/src/bridge.js` does.
4. Gate every action on the descriptor's `features`; absence means "this build
   cannot do that", not an error. Treat hunk ids as opaque and echo unchanged.
5. Skip following and hide actions for buffers with unsaved changes.
6. Heartbeat older than 30 s is disconnected; `activity` expires after 15 s;
   a request is only claimed done when its `id` appears in `lastRequest`.
7. Stdout is the LSP channel. Log to stderr (`window/logMessage` for
   user-visible errors). Never print anything else to stdout.

## Implementation plan

Work in this order; each phase leaves `go test -race ./...` green.

### Phase 1: wiring and consumer types

- `internal/app/app.go`: dispatch `args[0] == "editor-lsp"` to a new
  `internal/editorlsp.Run(args[1:], os.Stdin, os.Stdout, os.Stderr)` before
  the existing `editor-hook` / `--version` / `--help` checks. Parse `--ide
  <name>` (default `"lsp"`). Extend `--help` with one line.
- `internal/editorlsp/`: new package. Add a **consumer** side for the
  descriptors. The producer types already exist in `internal/editor`
  (`State`, `Change`, `ReviewState`, `ReviewFile`, `ReviewHunk`,
  `PendingRejections`, `RequestResult`, `Request`, `Features`); reuse them for
  decoding rather than duplicating. Put validation in
  `internal/editorlsp/descriptor.go` and port the checks from
  `extensions/vscode/src/bridge.js` (file: path confinement, oid shape,
  ranges, heartbeat/expiry math). Add table tests using the same fixtures the
  VS Code tests use (`extensions/vscode/test/*.test.js` shows the cases).
- Descriptor directory resolution: Git first — `git rev-parse --show-toplevel`
  then `git rev-parse --absolute-git-dir` — and, for a folder that is not a
  repository, `repo.Lookup`. Both halves are part of the protocol, so do not
  invent a third rule; see
  [editor-integration.md](editor-integration.md#discovery). Do not assume
  `.git` is a directory.
- Request writer + presence writer: reuse whatever `internal/editor` exposes
  for atomic owner-only writes; if that helper is unexported, export it rather
  than copying it.

### Phase 2: LSP transport and lifecycle

- Use a small JSON-RPC 2.0 over stdio implementation with the
  `Content-Length` framing LSP requires. Prefer no new dependency; if one is
  taken, `go.lsp.dev/jsonrpc2` + `go.lsp.dev/protocol` are acceptable and
  well-typed. Either way keep the transport in `internal/editorlsp/rpc.go`
  and unit-test framing.
- `initialize` result, `ServerCapabilities`:
  - `textDocumentSync`: `{ openClose: true, change: 2 (Incremental), save: { includeText: false } }`
  - `codeLensProvider`: `{ resolveProvider: false }`
  - `codeActionProvider`: `{ codeActionKinds: ["source.stvena"] }`
  - `executeCommandProvider`: `{ commands: [<list below>] }`
  - `inlayHintProvider`: `true`
  - Server info name `"stvena"`, version = build `Version`.
- Track per-document state from `didOpen`/`didChange`/`didSave`/`didClose`:
  URI → { repo-relative path, dirty bool, version }. Dirty becomes true on
  `didChange` and false on `didSave`. Zed reloads externally changed files and
  reports a `didChange` without `didSave`; treat that as dirty until a save is
  seen **unless** the buffer text equals the on-disk file (compare hashes on
  `didChange` for open files; this is what keeps agent-written files
  followable). Test this case explicitly.
- Poll loop (700 ms, matching VS Code): read both descriptors, validate,
  compute a diff against the previous snapshot, then push updates (Phase 3).
  Stop polling on `shutdown`; exit on `exit`.
- Presence heartbeat: write `stvena-ide.json` `{version:1, ide:<--ide>,
  extension:<Version>, updatedAt}` on `initialized` and every 20 s; delete or
  mark stale on `shutdown`. Mirror what the VS Code extension does on
  deactivate.

### Phase 3: rendering through LSP

Map each Stvena Live surface to its LSP feature. Publish on every descriptor
change and on `didOpen`/`didSave` of a relevant file.

- **Follow navigation**: when following is on, a new `activity` or a changed
  capture with a target file that is open-and-clean or not open, send
  `window/showDocument` `{ uri, external: false, takeFocus: false, selection:
  {start:{line-1,0}, end:{endLine-1 or line-1, 0}} }`. Newest event wins; read
  expiry never navigates back to an old edit. Deleted files never open.
- **Code lens** (`textDocument/codeLens`): for each hunk in the file from
  `stvena-review.json`, one lens at `hunk.start-1` with title depending on
  state: `✓ Accept` / `✗ Reject` for unreviewed; `✓ Accepted · Undo` for
  reviewed; `✗ Rejected · Undo` (or `Undo file rejection`) for rejected;
  `Reject file` for status `A` (added) files. Each lens carries a `command`
  from the list below with `arguments: [{path, hunkId, line, endLine}]`. Send
  `workspace/codeLens/refresh` after each descriptor change; because Zed's
  refresh handling has a reported gap (#58864), also re-publish diagnostics so
  the panel stays correct even if lenses lag.
- **Code actions** (`textDocument/codeAction`, kind `source.stvena`): using the
  request range: `Stvena: Review This Line in Stvena`, `Stvena: Add Selection
  to Context`, `Stvena: Ask the Agent About This Selection`, `Stvena: Accept
  All Changes in This File`, `Stvena: Reject All Changes in This File`,
  `Stvena: Apply Rejected Changes Now`, `Stvena: Pause Following` /
  `Resume Following`, `Stvena: Next Unreviewed File`. Each is a `Command`
  action (no edits). Show only actions the descriptor's `features` allow and
  only when the buffer is clean. "Ask the agent" has no prompt input in LSP:
  send `prompt` with an empty `text` and the range, and `showMessage` that the
  draft is in the agent input to complete.
- **Inlay hints** (`textDocument/inlayHint`): one hint at column 0 of the
  first line of each marker: `✎ agent edit`, `👁 read (<agent>)` or
  `👁 read (range unavailable)`, `◆ reviewing`. Padding right. Only the
  latest followed location is marked; clear after 15 s, on pause, disconnect,
  or dirty buffer. Send `workspace/inlayHint/refresh` on change.
- **Diagnostics** (`textDocument/publishDiagnostics`): one Information
  diagnostic per unreviewed hunk, range = hunk lines, message
  `Stvena: unreviewed agent change` (source `stvena`); rejected hunks as
  Hint `Stvena: rejected, reverts at <appliesAt>`; accepted hunks omitted.
  Publish for every file in the descriptor, open or not, so the Project
  Diagnostics panel is the review queue. Clear all on disconnect.
- **Status**: one long-lived `$/progress` token created with
  `window/workDoneProgress/create` on `initialized`; `begin` with title
  `Stvena`, then `report` messages like `following · 3 unreviewed` or
  `paused · 1 rejection waiting for turn end`; `end` on disconnect. Refused
  requests and capture errors go to `window/showMessage` (Warning).

### Phase 4: commands

`workspace/executeCommand` names, all prefixed `stvena.`: `accept`,
`unaccept`, `reject`, `undoReject`, `acceptFile`, `rejectFile`,
`applyRejections`, `nextUnreviewed`, `review`, `context`, `prompt`,
`toggleFollow`, `showLatest`. Every command except `toggleFollow` and
`showLatest` writes a `stvena-request.json` per `docs/editor-integration.md`
(version 1, session, unique id, action, path, line/endLine, optional hunkId,
optional text) and refuses when: not connected, action not in `features`,
target buffer dirty, or hunk id no longer present. Apply the optimistic state
locally (lens text flips immediately) and reconcile with `lastRequest`:
`applied` keeps it, `refused` rolls back and shows the message, no
acknowledgement within 60 s rolls back with "Stvena did not confirm".

### Phase 5: tests and docs

- Unit tests for descriptor validation, dirty tracking, lens/action/diagnostic
  computation from fixture descriptors, request-file content, and
  rollback-on-refusal. Use `t.TempDir()` Git repos with real `git` for the
  blob and git-dir paths, as the VS Code tests do.
- An in-process end-to-end test: start `editorlsp.Run` on pipes, drive it
  with a scripted LSP client (`initialize`, `didOpen`, write a fixture
  `stvena-review.json`, expect `publishDiagnostics` + `codeLens/refresh`,
  request `codeLens`, `executeCommand stvena.accept`, assert the request file).
  Print `STVENA_LSP_TESTS_PASSED` at the end like the VS Code host test.
- Update `docs/editor-integration.md` with a short "Language server consumer"
  section, and `README.md` install notes for Zed. Bump the presence descriptor
  docs to mention `ide: "zed"`.

## Commands you will run

```sh
go build ./... && go vet ./... && go test -race ./...
go build -o bin/stvena ./cmd/stvena && ./bin/stvena editor-lsp --ide zed < /dev/null   # must exit cleanly, nothing on stdout
# manual Zed check, in a disposable git repo with bin/stvena first on PATH:
#   zed: install dev extension → select ../zed-stvena
#   run ./bin/stvena in Zed's terminal in that repo; ask the agent to edit a Go file
#   dev: open language server logs → "Stvena Live"
```

## Acceptance checklist

- [ ] `stvena editor-lsp --ide zed` answers `initialize` with the capabilities above and exits on `exit`.
- [ ] `stvena-ide.json` appears with `ide: "zed"` within 30 s of opening a project; the TUI enters IDE mode.
- [ ] Saved agent edit → file opens at the changed line in Zed, terminal keeps focus, `✎ agent edit` hint appears, then clears after 15 s.
- [ ] Reported read (`Read` tool) → `👁 read (claude)` hint at the range.
- [ ] With `code_lens: on`: `✓ Accept` / `✗ Reject` above each hunk; clicking writes a valid `stvena-request.json`; the TUI's review mark changes; lens text updates on `applied`.
- [ ] A refused request (edit the hunk after the lens is drawn) rolls the lens back and shows the reason.
- [ ] Unsaved buffer: no follow, no lenses, no actions; save restores them.
- [ ] Project Diagnostics lists every unreviewed hunk; clearing on disconnect.
- [ ] Status bar shows following state and pending rejection count.
- [ ] A file in a language not in `extension.toml` gets nothing and the README says so.
- [ ] Nothing but LSP frames ever reaches stdout (`./bin/stvena editor-lsp < /dev/null | xxd | head` shows only `Content-Length` frames or nothing).
- [ ] `go test -race ./...`, `go vet`, `npm test` in `extensions/vscode` all pass; VS Code behavior is unchanged.

## Open decisions (pick and document, do not block)

- Whether unreviewed hunks should be Information (visible in panel, underline) or Hint (quieter). Default to Information; make it an initialization option if it proves noisy.
- Whether to add `workspace/didChangeWatchedFiles` on the Git dir instead of polling. Polling matches VS Code and the 700 ms budget; keep polling unless CPU is measurable.
- Language list in `extension.toml`: verify names against the Zed you test with and trim anything that errors.

# Accept/reject QA — 2026-09-15

The original QA run tested commit `f09e989` (the 39-file accept/reject release)
and reproduced the four defects recorded below. All four are now fixed in the
local working tree; the original regression tests and the expanded safety cases
pass. The extension version and lockfile are 0.4.0. No release has been published.

## Fixes

- Group queued hunks by file and captured revision. Each capture keeps its own
  patch section; all sections still run through one atomic Git apply. Whole-file
  rejection continues to supersede hunk rejections of that file.
- Validate an added file's hunk token, then queue its whole-file deletion. The
  inline label explicitly says “Reject file.” Stale tokens are still refused.
- Let an affected hunk undo its pending whole-file rejection, with the label
  “Undo file rejection” and a response identifying the whole-file scope.
- Preserve Git failure and staged-copy notices in the bridge acknowledgement.
  The extension tracks explicit apply requests and displays their acknowledged
  outcome once, including refused reverts after the queue has cleared.
- Make the host test wait for the selected line, rather than treating editor
  visibility as proof that asynchronous navigation has finished.

## Verification after the fixes

- `go test -race ./...`: passed, including the original four regressions.
- Mixed-capture safety cases passed for unstaged and staged files, inserted
  lines, and stale later targets. A stale target leaves both the index and
  working tree unchanged; successful staged reverts keep them in agreement.
- `go vet ./...`, `go build ./...`, and `git diff --check`: passed.
- `npm test` in `extensions/vscode`: all 22 tests passed.
- Packaged `stvena-live-0.3.1.vsix`, extracted its extension, and ran the expanded
  host suite against those packaged files in fresh disposable workspaces.
  Both VS Code 1.137.0 and Antigravity IDE 2.5.5 emitted
  `STVENA_HOST_TESTS_PASSED` on their first run after these fixes. The new host
  checks cover whole-file Undo labels, retained hunk tokens, and added-file
  rejection labels.
- `TestEditorApplyRejectionsPreservesStagedCopyWarning` also passed under the
  race detector after the full suite completed.
- Real model-driven Codex/Claude hook sessions were not exercised in this pass;
  boundary behavior is covered by the existing Go tests.


## Independent verification for the fix PR — 2026-09-15

- Re-ran `go test -race ./...`: all packages passed, including the regression
  tests and the staged-copy warning test.
- Re-ran `npm test`: 22/22 passed. `go vet ./...`, `go build ./...`, Go
  formatting, installer shell syntax, and `git diff --check` passed.
- Rebuilt the 0.3.1 VSIX, extracted it, and added only the host test runner to
  its test directory. Both installed IDEs emitted `STVENA_HOST_TESTS_PASSED`
  against those packaged runtime files in disposable Git workspaces with
  isolated profiles and extension directories.
- VS Code 1.137.0 initially could not start because macOS rejected the long
  profile socket path (`EINVAL`). A shorter `/tmp/stvqa-*` profile path resolved
  the launcher failure; the host suite then passed.
- Antigravity IDE 2.5.5 passed on its first run. Its built-in onboarding emitted
  a migration-dialog warning during testing; the Stvena suite completed and
  the extension host exited with code 0.
- Updated the extension guide to describe added-file rejection, whole-file
  Undo, confirmed apply outcomes, and the current source-build VSIX filename.
- Live model-driven Codex/Claude hook execution and independent human QA remain
  unverified. Coverage deltas were not measured; PR CI is tracked separately
  in GitHub checks. No Marketplace release or GitHub release was published.


## Original baseline

- `go test -race ./...`: passed before adding the regression tests.
- `go vet ./...` and `go build ./...`: passed.
- `npm test` in `extensions/vscode`: all 19 tests passed.
- VS Code 1.137.0: host suite emitted `STVENA_HOST_TESTS_PASSED`.
- Antigravity IDE 2.5.5 (VS Code base 1.107.0): first run failed at
  `test/host.js:72`, with cursor line 0 rather than 3. A second run in another
  fresh profile emitted `STVENA_HOST_TESTS_PASSED`. This is an intermittent
  navigation/assertion failure; the cause is not established. The assertion
  waits for editor visibility, then immediately checks the selection.

Both hosts used isolated profiles, empty extension directories, disposable
`stvena-editor-project` workspaces, and the source extension. These checks do
not verify a packaged VSIX or a real Codex/Claude turn-hook session.

## Projects without a Git repository, 2026-09-15

The host suite now runs its whole scenario twice: once against a Git repository
and once against the same folder with no repository, resolved through Stvena's
bridge registry. Results, source extension, isolated profiles as above:

- `go test -race ./...`, `go vet ./...`, `go build ./...`: passed.
- `npm test` in `extensions/vscode`: 28 tests passed.
- VS Code 1.137.0: `STVENA_HOST_TESTS_PASSED … git mode · shadow mode`, on two
  consecutive runs in fresh profiles.
- Antigravity IDE 2.5.5 (VS Code base 1.107.0): same, one run.

One failure during development was worth recording, because it was the fixture
and not the product: with both passes sharing filenames, an editor kept a
document for a file the first pass had opened, and a stale buffer answered an
assertion about what the second pass had just published. Each pass now uses its
own session and its own filenames. An earlier attempt to delete the files
between passes was worse: deleting a file an editor still holds a document for
marks that document unsaved, and an unsaved buffer deliberately blocks automatic
navigation.

The intermittent Antigravity navigation failure recorded above was not seen in
these runs. Failures now name the pass, so a failure that only happens without
Git cannot be triaged as that known flake.

## Defects reproduced before the fixes

### P1: Queued rejections can revert the wrong hunk

Source: `internal/review/reject.go:145`, especially lines 159–160.
Test: `TestRejectionQueuePreservesTargetsAcrossCaptures`.

1. Capture a file with separate changes on lines 20, 40, and 60.
2. Reject line 20 (hunk 0).
3. While the queue waits, add another independent change on line 1 and capture.
4. Reject line 40, now hunk 2.
5. Apply the queue.

Actual: lines 20 and 60 are reverted; line 40 remains. The apply returns success.
The queue groups by file key, retains the first captured patch, and appends
ordinal hunk indices from later captures. Hunk 2 in the old patch is line 60.
Content-addressed editor IDs do not protect this later grouping step.

Expected: revert exactly the captured rejected hunks, or refuse the batch if
those exact targets cannot be applied safely. Never reinterpret an index using
another capture. Preserve atomicity when repairing this.

### P2: Inline Reject on a new file cannot apply

Sources: `internal/app/editor.go` (published hunks and rejection handling),
`internal/diffview/stage.go` (`selectHunks`).
Test: `TestEditorRejectLensOnNewFileRemovesTheFile`.

An added file exposes a Reject lens with a hunk ID. Clicking it queues that hunk.
At application time the backend refuses with “reject added, deleted, renamed
and mode changes as a whole file.” The file survives and the queue becomes
unreverted history. The test uses a real Git addition represented in Session
scope, matching the editor's review source.

Expected: offer a supported whole-file action for additions, or refuse the
unsupported action before presenting it as queued. The ordinary Reject lens
must not promise an operation the apply layer cannot execute.

### P2: Reject All exposes an Undo action that is refused

Source: `internal/app/editor.go:287`.
Test: `TestEditorWholeFileRejectionCanBeUndoneFromItsHunkLens`.

Reject All queues a whole-file rejection and publishes every hunk as rejected.
Each hunk lens offers Undo using its hunk ID. The backend only matches that ID
against hunk-level queue entries and responds “No pending rejection for that
change.” The whole-file rejection stays queued.

Expected: provide a working undo action for the published rejection, with clear
whole-file semantics if that action cancels the entire file rejection.

### P2: Explicit apply acknowledges success after a failed revert

Source: `internal/app/editor.go:84`.
Test: `TestEditorApplyRejectionsReportsFailure`.

Queue a rejection, change the target lines again, then request
`apply-rejections`. Git correctly refuses the stale patch. However, the editor
handler unconditionally overwrites the failure notice and publishes status
`applied`, message “Applied 1 rejection(s) from the editor.”

Expected: acknowledge that the revert failed and the agent handoff was queued;
never claim the changes were reverted. This does not invalidate the existing
agent-draft fallback, which is tested separately.

## Run the regression cases

The tests in `internal/app/reject_regression_test.go` preserve the original
reproductions and add stale-batch, index, inserted-line, and stale-click checks.
Run:

```sh
go test -race ./internal/app -count=1 -run 'TestRejectionQueuePreservesTargetsAcrossCaptures|TestEditorWholeFileRejectionCanBeUndoneFromItsHunkLens|TestEditorApplyRejectionsReportsFailure|TestEditorRejectLensOnNewFileRemovesTheFile'
```

All four original regression cases now pass. The additional staged-copy warning
test verifies that a successful working-tree-only revert does not hide the
remaining staged change.

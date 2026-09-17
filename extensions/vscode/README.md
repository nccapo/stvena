<p align="center">
  <img src="media/icon.png" alt="Stvena Live icon" width="128" height="128">
</p>

# Stvena Live

Run Stvena in your editor's integrated terminal and follow agent reads and saved
edits in the actual source file, while keyboard focus stays in the terminal.
Blue eye markers identify reported read ranges; amber pencil markers identify
changed lines. Both include a line tint, an inline label, and a scrollbar marker.
Markers expire after 15 seconds and clear on pause, disconnect, or unsaved edits.

## Install from the VS Code Marketplace

Requires Git, macOS or Linux, VS Code 1.85 or newer, and an installed,
authenticated Codex or Claude Code CLI. Install both the extension and the
compatible Stvena terminal application:

1. Install [Stvena Live by nccapo](https://marketplace.visualstudio.com/items?itemName=nccapo.stvena-live)
   from the Extensions view, or run:

   ```sh
   code --install-extension nccapo.stvena-live
   ```

2. Install the Stvena binary, 0.4.0 or later, with Homebrew or the install
   script:

   ```sh
   brew install nccapo/stvena/stvena
   ```

   ```sh
   curl -fsSL https://raw.githubusercontent.com/nccapo/stvena/main/install.sh | sh
   ```

3. Open a trusted local project, with or without Git. In the integrated terminal, check
   `stvena --version` reports `0.4.0` or later, then run `stvena` for Codex or
   `stvena claude` for Claude Code.
4. Ask your agent to edit and save a file. Its change blocks are tinted in the
   working file with **✓ Accept** and **✗ Reject** above them, and **Stvena
   Live** in Explorer lists the captured edit while focus stays in the terminal.

No Go or Node.js installation is needed. Marketplace installations receive
extension updates through VS Code according to your update settings. Update the
Stvena binary separately (`brew upgrade stvena`, or rerun the install script).

### Manual installation and Antigravity

Download `stvena-live-0.4.1.vsix` from the
[v0.4.0 release](https://github.com/nccapo/stvena/releases/tag/v0.4.0),
or [build the current extension from source](#build-from-source). In VS Code or
Antigravity IDE's Command Palette, run **Extensions: Install from VSIX…** and
select the package, then follow the binary installation and startup steps above.
To update a manually installed VSIX, install the newer package.

## What's new in 0.3.0

**Accept or reject the agent's changes without leaving your editor.** Every
change block the agent made is tinted in the working file, with **✓ Accept** and
**✗ Reject** above it and a badge in the Explorer counting what still needs
review. It is still your ordinary source file, never a diff tab.

Rejecting reverts those exact lines and tells the agent what you turned down, so
it does not write the same thing again. It does not happen the instant you click:
reverting a file while the agent is mid-turn makes its next edit build on lines
that no longer exist, and often makes it re-apply the very change you rejected.
The rejection is queued, shown immediately as rejected, and applied when the
agent finishes its turn. The status bar says how many are waiting and why, and
clicking it applies them now.

Requires Stvena **0.4.0** or newer. Against an older binary the
extension keeps its 0.2.x read and edit following and hides what that binary
cannot do. See the [changelog](CHANGELOG.md).

## Accept and reject

| Where | Action |
| --- | --- |
| Above each change block | **✓ Accept**, **✗ Reject**, **Reject with reason…** |
| Above a decided block | **✓ Accepted** / **✗ Rejected**, each with **Undo** |
| Editor context menu | Accept or reject every change in the file |
| Status bar | Pending rejections; click to apply them now |
| Explorer | A badge per file: blocks left to review, ✓ reviewed, ✗ rejected |

New files show **Reject file**, because rejecting an addition removes the whole
file. After a whole-file rejection, **Undo file rejection** on any affected
block cancels that file's queued rejection. **Apply Rejected Changes Now**
reports the confirmed result, including failed reverts and any changes left
in the staging index.

Accepting is the same review mark as **Space** in the terminal, and rejecting
uses the same queue as **X**, so the two surfaces always agree.

**Reject with reason…** is the one worth using. The reason reaches the agent in
the handoff message, so "this breaks the existing session contract" prevents a
repeat in a way that a bare rejection does not.

Decisions appear straight away and are confirmed a moment later, because Stvena
is polled rather than pushed. A decision Stvena refuses — most often a block the
agent changed again after your editor drew it — is rolled back with an
explanation rather than silently kept.

Markers and actions are hidden while a file has unsaved changes, since the
captured lines no longer match what is on screen. Save, and they return. Set
`stvena.showCodeLens` to false to keep the tinting without the buttons.

## Troubleshooting

If the view stays **Waiting**, check the binary version, workspace trust, and
that Stvena is running in the same project. Use **Stvena Live** in the Output
panel for connection errors. Read markers depend on supported agent hooks;
saved-edit following works independently of them.

## Build from source

1. Build the accompanying Stvena binary from this repository:
   `go build -o bin/stvena ./cmd/stvena`.
2. In `extensions/vscode`, run `npm ci` and `npm run package`.
3. In VS Code, run **Extensions: Install from VSIX…** and select the generated
   `stvena-live-0.4.0.vsix`.
4. Open a project and run the newly built Stvena binary in its terminal.
   Released binaries predating this integration do not publish editor updates.
5. Ask your agent to edit a file. **Stvena Live** in Explorer lists the latest
   captured batch; selecting a file opens its working source at the changed line.

## Using Stvena Live

Click **Stvena: Following** in the status bar to pause automatic navigation.
Navigation pauses while the activity list and saved source continue updating.
Click again to resume. **Stvena: Show Latest Activity** opens the latest read or changed file.
Automatic following skips unsaved buffers so their content and cursor stay intact.

The bidirectional review features below currently require source builds of both
the extension and the accompanying Stvena binary. Published 0.2.x builds provide
the read/edit following described above.

When the TUI is inside a file, a purple review marker follows its current source
line or selected range. It is persistent while that review location is active;
Stvena still renders the diff, and the extension opens only the ordinary working
file. From an editor context menu or the Command Palette:

- **Stvena: Paste Selection to Agent** puts the captured lines into the agent's
  input, like `b` in Stvena, without sending them. A selection also shows
  **⤴ Paste to agent · Ask… · Add to context** above its first line, and a send
  button in the editor title bar. Set `stvena.showSelectionCodeLens` to `false`
  to hide the actions above the selection.
- **Stvena: Review This Line in Stvena** opens the corresponding captured change
  in the TUI, falling back to the captured project file when it is unchanged.
- **Stvena: Add Selection to Context** adds the matching immutable captured lines
  to Stvena's saved context tray. Save the editor buffer first.

## Following reads

Stvena adds a `PostToolUse` observer to standard `stvena codex` and `stvena claude`
launches, including new agent terminals. Use the compatible binary above
or, when building from source, rebuild Stvena as well as the extension.
For Codex versions with hooks, open `/hooks` and review/trust the Stvena observer
when prompted. Stvena does not bypass hook trust or tool permissions. Hooks must
be enabled by the agent; managed restrictions, safe/bare modes, and older agent
versions may prevent read reporting. Saved-edit following still works.

Supported reads are Claude's `Read` tool (`file_path`, `offset`, `limit`) and
simple, single-file shell commands: `sed -n '40,65p' file`, `head -n 25 file`,
and `cat file`. Quoted paths are supported. Compound commands, pipes, shell
expansions, search results, and custom tools are skipped rather than assigned
an invented range. A read without an explicit end is labeled “range unavailable”
and gets a marker at the starting line. Ranges describe the tool's requested
input after it returns, not model attention or proof that every line was read.
Long-running reads appear only on completion. Source may have changed since then.

The latest read from any supported terminal in this Stvena session is shown,
with the agent command name. Simultaneous activity can coalesce. Paths outside
the repository (including symlink targets) are excluded. No conversation logs
are scanned or copied.

When launching Claude with your own `--settings`, Stvena preserves that option
and does not inject another settings argument. Add this observer to your existing
settings to enable reads (use the absolute path to the rebuilt binary):

```json
{
  "hooks": {
    "PostToolUse": [{
      "matcher": "Read|Bash",
      "hooks": [{ "type": "command", "command": "'/absolute/path/to/stvena' editor-hook", "timeout": 2 }]
    }]
  }
}
```

The hook is silent and only publishes activity when launched under Stvena.
Agent integration follows the [Codex hooks](https://developers.openai.com/codex/hooks)
and [Claude hooks](https://code.claude.com/docs/en/hooks) interfaces.

## Scope and compatibility

- Targets the VS Code extension API, version 1.85 or newer. Other editors must
  support that API and VSIX installation; compatibility needs testing per editor.
  The editor-host smoke test passed on macOS in stock VS Code 1.137.0 (Apple
  Silicon) and Antigravity IDE (VS Code base 1.107.0): source navigation, line
  selection, pause/resume, additions/deletions, and unsaved buffer preservation.
  Other forks have not yet been exercised in an editor host.
- Zed uses a separate extension API and cannot install this VSIX. See
  [Zed's extension documentation](https://zed.dev/docs/extensions/developing-extensions).
- Git and Stvena must run on the same machine as the workspace extension host.
  Local macOS and Linux are the initial target. Remote hosts are unverified.
  Stvena itself does not support native Windows.
- Open a trusted local workspace. Nested folders, multiple workspace roots, and
  Git worktrees are supported by project discovery.
- A project that is not a Git repository works too: Stvena captures it into its
  own directory and records where, and the extension finds it there when Git has
  nothing to say about the folder. Git is always asked first, so a repository is
  resolved exactly as before. Branch comparison and staging need a repository and
  are unavailable in such a project; following, review focus, markers and
  accept/reject are not. The `git` command itself is still required.
- Edit updates are snapshots of **saved files**, sampled roughly every 700 ms plus
  capture time; extension polling adds up to another 700 ms. This is not a stream
  of the agent's keystrokes or an exact audit history. Multiple writes can combine
  into one capture, and a slow/disconnected editor can skip intermediate captures.
- All workspace writers appear, including you, formatters, and other agents.
  Edits cannot yet be attributed to a particular agent. Files in a batch have Git
  order, not a proven edit order; following prefers the current file if it changed.
- Changed lines come from consecutive captures; the editor displays the current
  working file, which may be newer. Preexisting edits are excluded from a new
  session's first capture. The terminal retains cumulative session review.
- Deleted files stay listed but do not open an editor. Renames follow the new path.
- Binary edits stay listed without automatic text previews. Text blobs have a
  16 MiB limit; oversized capture patches can report a capture error. Ignored
  untracked files follow Stvena's capture rules.
- Use one Stvena process per repository. The view reports waiting after a normal
  shutdown, or after 30 seconds without a heartbeat following a crash/stall.

## Local data

The extension reads private `stvena-live.json` and `stvena-review.json`
descriptors and writes validated user actions atomically to
`stvena-request.json`. Those descriptors live inside the worktree's Git
directory, or, for a project that is not a repository, in Stvena's own
per-project cache directory — never in your source tree. It locates the second
kind through Stvena's bridge registry in `~/.stvena/bridges/` (or `$STVENA_HOME`),
one owner-only JSON file per reviewed project holding the project's path and the
directory its descriptors are in. Nothing in that file is ever executed. It opens working source files. There is no listening
network service, account, or telemetry. Stvena's tool observer stores only the
latest read location in its private session cache and includes it in the bridge.
The extension neither writes source files nor runs terminal commands on the
agent's behalf.

For connection or preview errors, select **Stvena Live** in the Output panel.

## Development

`npm test` exercises activity validation, marker lifecycle, real Git/worktree/blob
reads, and project discovery with and without Git.
The extension is plain JavaScript with no runtime npm dependencies. Launch an
extension development host with `--extensionDevelopmentPath=/absolute/path/to/extensions/vscode`.
See [the bridge protocol](../../docs/editor-integration.md) for integration details
and the editor-host smoke test procedure.

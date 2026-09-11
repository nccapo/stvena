# Stvena user guide

Run Codex or Claude with an interactive review workspace in the same terminal.
See what changed, read the complete file, leave feedback, and check the code
without losing the CLI's native colors or conversation.

## Start

Requires Go 1.24.2+, Git, a terminal, and your chosen agent CLI on `PATH`.

```sh
go install ./cmd/stvena
cd /path/to/your/git-project
stvena                 # Codex by default
stvena claude
stvena -- codex --model YOUR_MODEL
```

The shortcuts below use **Ctrl** on both macOS and Linux. Command keys belong
to the terminal application unless it is explicitly configured to forward them;
see [Command keys in your terminal](#command-keys-in-your-terminal).

Stvena starts with **Configuration** selected in the bottom panel. Press
**Enter** to adjust shortcuts, or **Esc** to focus the agent and paste your prompt. **Ctrl-G** switches panes. Click a file
or select it with arrows and Enter. **v** switches between changed lines and the
whole file. **a** opens the Actions menu; the common controls stay at the bottom.
You can also click the **Changes** and **Full file** tabs. File rows put names
first, with folders, readable change status and line counts alongside them.
Colored letters before each filename identify **M** modified, **A** added/new,
**D** deleted, **R** renamed, **C** copied, and **U** conflicted files.
The bottom of the review pane adapts to browsing, reading or selecting code;
additional tools remain in **Actions** without filling the toolbar.
**Ctrl-G** switches between the agent and your open file, preserving the view
and selection so you can paste another slice. **Ctrl-Q** closes stvena and
stops all running agents from either pane. **Ctrl-C** keeps its native agent
interrupt behavior. Review stays open when an agent finishes; **q** closes it
once all agents finish.

While the agent is running with no changes to show, the review pane displays an
stvena logo, a gently pulsing status dot, and a quick guide to the controls.
Changed files appear automatically as soon as they are captured.

```sh
stvena review                  # Reopen the latest session baseline
stvena sessions                # List saved session IDs
stvena review --session ID     # Compare that baseline with today's workspace
```

Standalone review opens fullscreen and continues observing the working copy.
It does not resume an agent conversation. Starting outside Git still launches
the CLI; the pane detects a later `git init`, with basic workspace review.

## Multiple agent terminals

Press **Ctrl-]** from either pane to choose a command for a new terminal:
**c** starts Codex, **l** starts Claude Code, and **Enter** repeats the original
launch command with all its arguments. You can also use **↑/↓**, then **Enter**,
or click an option. **Esc** cancels and returns to the previous pane.

For example, start `stvena codex`, then press **Ctrl-]**, **l** to run Claude Code
in a second terminal alongside Codex. Each new terminal starts in the original
working directory. The Codex and Claude Code options use the CLI defaults and
require that CLI on `PATH`; arguments from the original command only apply when
you choose **Launch command**. A failed launch leaves the chooser open so you can
choose another command or cancel.

**Ctrl-N** selects the next terminal;
**Ctrl-P** selects the previous one, wrapping around at either end. The header
shows the active command and terminal number. These controls are also clickable
in the footer while review has focus.

**Ctrl-W** closes the current agent terminal, stops its command, and selects the
previous terminal (wrapping around). Closing the last terminal leaves the review
workspace open; **Ctrl-]** starts a new agent, and **q** exits.

Each terminal keeps its own screen, conversation and unfinished input. Background
agents keep running; switching brings the selected agent into focus. Review code
handoffs go to the selected agent. Finished terminals remain available to inspect.
**Ctrl-C** interrupts the focused agent; **Ctrl-Q** stops all agents and exits.

All agent terminals share the working directory and one review workspace and
session baseline. Changes from every agent appear together in review. These are
live terminals, not saved conversations; reopening review does not restart them.
The terminal shortcuts are unavailable in standalone `stvena review`.

## Browse project files before prompting

Click **Files** at the bottom or press **3** in review to browse all files in the
captured project, including unchanged files, in an expandable folder tree.
Folders appear first and start collapsed. Click a folder or press **Enter** to
expand/collapse it; **Right** expands or enters a folder, and **Left** collapses
it or selects its parent. Click a file or press **Enter** to open it. **/** filters
full paths and reveals matching files inside their folders without changing your
normal folder expansion state. In an open file, **/** searches its text,
and **f** returns to the tree with that file revealed. **1** returns to session changes; **2** opens
workspace changes. The project browser does not add unchanged files to review
counts or allow staging them from that view.

The browser reads the captured Git tree, including nonignored new files and
tracked ignored files. Ignored untracked files and submodule contents are not
listed. Files open from captured blobs, so a live edit cannot silently replace
a collected selection. Project browsing needs a successful session capture;
if Git was initialized after launch, restart stvena to enable it.

## Build a request with several attachments

Select code and press **x** (**Collect**) to keep a copy in the context tray.
Repeat in other files, then press **B** or click **Context** at the bottom.
The tray lists each selection with its path and line range. **i** edits your
request, **Enter** previews the complete assembled draft, and **b** pastes it
into the running agent. Additions carry absolute paths, source ranges and the
captured version. The original **b** shortcut outside the tray still pastes
just the current selection immediately.

Use arrows or click an attachment to select it; **d** / Backspace removes it.
**y** in the tray or preview copies the complete draft for another chat. The
tray and request are saved locally and remain available after pasting or
restarting stvena. Nothing is submitted until you press Enter in the native
CLI. The tray supports up to 20 attachments and a combined 32 KiB draft;
duplicate identical attachments are rejected. An earlier-snapshot label means
the workspace has advanced; it does not prove that particular slice changed.

## Navigate check failures

Open **Checks** (**T**), then **Problems** (**o**) to see recognized source
locations from the latest check. Click a location or press **Enter** to open
its tested source at the reported line. **x** collects the diagnostic, command,
tested version and nearby captured source into the context tray. If source is
unavailable or a filename is ambiguous, the failure can still be collected
with that limitation stated explicitly. **Esc** returns to the complete logs.

The initial parser supports `file:line[:column]` diagnostics (including common
Go and Rust output), TypeScript `file(line,column):` locations, and Python
traceback file locations. It normalizes absolute paths inside the temporary
checkout. Unrecognized output remains in the raw logs. A Problems entry is a
recognized location, not a severity classification or a count of failed tests.
Generated files may not exist in the starting snapshot. If the check changed
source, the viewer identifies that the starting source may have different lines.

**t** opens the run/rerun prompt, prefilled with the last command. Enter explicitly
starts a check against the currently displayed captured tree; **P** or **3**
returns to current project files when you are viewing an older tested version.

## What you can review

**This session (1)** compares the working copy captured before the CLI starts
with later captured versions. Existing dirty and untracked files are part of
the baseline, so they do not look like new session work. Changes remain visible
after commits. This records changes observed during the session, including edits
from another editor or terminal; it does not identify which process made them.

**Workspace (2)** shows staged, unstaged and untracked changes separately. Tab
filters those scopes. A file may appear twice if it has both staged and unstaged
edits. The top file count counts unique paths; line totals sum the displayed
scopes. Renames show both paths, and binary/conflicted/incomplete previews are
explicitly labeled.

**Project files (3)** browses every nonignored file in the latest captured tree,
including unchanged files. **Branch changes (4)** compares the captured working
tree with the merge base of `HEAD` and the locally configured/default
`origin/main`, `main`, `origin/master`, or `master`. It includes committed and
working changes and never fetches or switches branches.

The code viewer provides:

- Syntax colors in unified diffs and complete files, with old/new line numbers.
- Side-by-side comparison with changed character spans emphasized; long lines
  can wrap in either comparison mode. Narrow panes fall back to unified display.
- Full-file context, including unchanged lines and the version before deletion.
  Staged files use the captured index blob. Worktree/session files use captured
  Git content when available. The source is labeled above the code.
- Path filtering, source text search, source-line jumps, hunk navigation and
  horizontal scrolling. Diff/full-file toggles keep the corresponding source
  location, or the nearest visible patch line.
- Fullscreen review, adjustable pane proportions, and saved wrap/comparison/
  width preferences. Mouse clicks open files, scroll review and activate bottom
  controls or Actions while review has focus. Left-button dragging selects code. Shift-drag can select terminal
  text in terminals that reserve Shift for native selection.

## Review an evolving change

After the agent edits `api.go` and `api_test.go`, press **Ctrl-G**, then choose
**Actions → Review checkpoint** (**K**). The current captured session tree stays
pinned while you use **Space** / **H** to mark files / hunks and **N** to find the
next unreviewed file. Select an exact range in each file, use **c** to comment or
**x** to collect code, and optionally add your request in **B**, then **i**.
The header counts reviewed files/hunks, saved comments and tray selections;
**NEW LIVE** means a newer capture exists and has not been reviewed.
Choose **Finish checkpoint** (**Z**) to preview the combined draft. If review is
incomplete, **Esc** returns to review; **Enter** previews anyway. In the preview,
**b** prepares one Codex/Claude Code input without pressing Enter; standalone
review copies it instead, and **y** always copies. Return with **Esc**, then
**P** to resume live and review changed items again. Closing and reopening a
saved review preserves the pinned checkpoint, marks, feedback and draft.

Checkpoint drafts include comments anchored to that checkpoint tree and all
items currently in the existing context tray, including their original paths,
line ranges, code and versions. Remove unwanted tray items with **B**, then
**d** before finishing. Source switching, history comparisons and tested-source
navigation require resuming live first, so they cannot replace checkpoint code.
The assembled draft has the same 32 KiB limit as context handoffs.

**P** pins the displayed version while the agent keeps running. An update badge
shows when newer changes arrive; P resumes live updates. Full-file content stays
on the pinned version too.

**Space** marks a file version reviewed. **H** marks the current hunk. Marks are
saved locally; changed versions need review again. `✓` means reviewed, and `↻`
means there is review history but the displayed change needs review. **R**
compares an existing captured file with the version last marked reviewed. This
comparison currently requires both file versions to exist. Incomplete previews
and conflicts cannot be marked reviewed.

**N** (or **Next unreviewed** at the bottom) opens the next file that still needs
review, skipping reviewed versions and wrapping within the current filter.
Mark a file with **Space**, then use **N** to continue. Files changed again by
the agent need review again and return to this queue.

**I** toggles Review inbox, which hides reviewed file versions and counts the
remaining files, hunks, and items changed again. **L** opens the session timeline.
Each entry is a stable tree transition observed by Stvena, not a claimed agent
turn or author. Enter pins that batch for inspection; the header counts newer
batches, and **P** returns to the live cumulative session diff. Up to 100 batches
are retained per saved session.

The top visible source line is the keyboard selection. **V** starts a range;
move to extend it. Selection pins the version. **y** copies selected source text,
and **c** attaches a comment to that version and source range. Comment on old
and new sides separately when a range spans a replacement. **C** shows saved
comments and **E** copies the feedback with paths, line ranges, code and snapshot
identifiers. Comments whose recorded change differs from the live view are
flagged outdated. Feedback is copied for you to inspect and paste into the CLI.

**Y** copies the complete path. **e** opens the current working file at a line
using the first available `code`, `cursor` or `zed` shell command. Historical
views still open the current file in the editor; deleted working files cannot
be opened. Clipboard actions use `pbcopy` on macOS, or `wl-copy`, `xclip` or
`xsel` on Linux.

## Ask the agent about selected code

Open a file and **drag with the left mouse button across the code lines**.
Release the button, then press **b** (**Paste selection to agent**) in review or
click its bottom control. The selected code is inserted into the running CLI's
input and focus returns to the agent. **Type your request and press Enter there.**
Mouse selection highlights whole source lines and pins their version while the
agent keeps working. Dragging does not move the viewport. Wrapped continuation
rows select their original line; side-by-side drags stay on the starting side.
Scroll the code pane with the wheel to reach more lines while selecting, or
Shift-click to extend a selection if your terminal forwards Shift-click. Release
outside the pane ends the drag without activating another control. **V** clears
the selection and resumes live updates. Leaving the selection to browse files or
switch between Changes and Full file also resumes updates. A version explicitly
pinned with **P** stays pinned until you choose **Resume live**.

While selecting, the pane shows the selected range and bottom controls for
**Add to agent**, **Copy**, **Clear**, and **Resume live**. Adding to the agent
prepares a draft; you still type your question and send it in the native CLI.
Submitting that draft with Enter releases the selection's temporary pin, so
the agent's next edits appear automatically. Until then, you can return to the
same selection and add more code to the draft.

Keyboard selection still works: move to the first source line, press **V**, then
use arrows to extend the range. You can also paste the current source line
without starting a range.

The draft includes the absolute file path, repository-relative path, source line
ranges and captured version so the agent can locate the file directly. Diff
selections keep `+`/`-` prefixes so removed and added code remain distinguishable;
full-file selections contain ordinary source code. Repository terminal controls
are replaced with visible characters. The action preserves existing CLI input,
does not press Enter, and does not send a separate message behind the scenes.
Typing that arrives during the handoff is buffered and forwarded afterward.

Direct handoff works when launched with `stvena codex` or `stvena claude`
(including absolute executable paths) and the CLI has enabled bracketed paste.
Use it with the CLI's chat input ready. The app does not infer whether the agent
is busy or displaying a dialog. Standalone review, other commands and wrappers
such as `stvena sh -c ...` can use **y** to copy code instead. Selections over
32 KiB must be shortened or copied. If a paste fails, inspect the CLI before
retrying because it may have received part of the draft.

## Run checks on captured code

Press **t**, enter a shell command and press Enter. For example:

```sh
go test ./...
npm ci && npm test
```

The command runs in a temporary checkout of the selected captured Git tree,
with your normal user permissions and environment. It is not a sandbox. Ignored
dependencies, the repository's `.git` directory and submodule contents are not
copied. Git clean/smudge filters still apply. Dependencies can be installed by
the supplied command. Snapshots with unresolved or external symlinks are rejected
for checks so those links cannot silently substitute live source files.

**T** displays the command, captured version, completion time, exit code and
output. Results are saved, and the header marks them outdated when the current
working-copy tree differs. If a check changes an existing captured source file,
that is reported alongside its command result. A passing command describes its
run from that starting snapshot; it does not prove environment reproducibility
or validate files generated during the command against the original snapshot.

Commands have a 15-minute limit and a 1 MiB output limit. Quitting cancels the
active check and removes its temporary checkout. No test command runs
until you explicitly submit one.

## Stage deliberately

In **Workspace**, **S** stages/unstages the selected file and **A** stages/
unstages the selected hunk. A confirmation identifies the file and operation;
Enter confirms and Esc cancels. Git verifies the displayed patch before changing
the index, and the app rejects a file that changed since the view was collected.
Working files are preserved.

Hunk staging supports ordinary text modifications. New, deleted, renamed and
mode-changing files must be staged as whole files. Conflicts, incomplete patches
and existing tracked binary-file changes need another Git client. New binary
files can be staged as whole captured files. Resume live updates before staging.
There are no discard, restore, commit, push or branch-editing actions.

## Controls

The bottom panel has focus when Stvena opens, with **Configuration** already
selected. Press **Enter** to configure shortcuts immediately, or **Esc** to start
typing in the agent (or return to review in standalone mode). No Ctrl combination
or function key is needed to reach Configuration on startup.

Use **Left/Right** to select a footer control, **Up/Down** to move between wrapped
rows, and **Enter** to activate it. **Esc** returns to the pane. **Down** at the
end of a review file list also enters the panel; **Up** from its first row
returns to the pane. Ordinary arrow keys in the agent pane remain available to
its editor and history after leaving the footer. **F6** remains an optional way
to refocus the panel; keyboards with media controls may require **Fn-F6**.

Select **?: Configuration** in the panel to edit shortcuts. It opens on
**Switch panes**, so a Ctrl-G conflict never prevents configuration. For example,
press **Enter**, then **Ctrl-O** to change pane switching from Ctrl-G to Ctrl-O.
Ctrl-G is then passed through to the agent. **?** in review also opens Configuration.

Select another action with **↑/↓** (or **j/k**) and press **Enter**, or click its
row. Global actions (switch panes, new/next/previous/close agent, quit all) accept
Ctrl combinations. Review actions accept one printable key; uppercase and
lowercase are distinct. Conflicts are rejected. Ctrl-C, Ctrl-D/U, the control
bytes used for text-entry/navigation keys, and F6 cannot be assigned as global
actions. **Esc** cancels an edit or closes Configuration. **Backspace** restores
the selected shortcut; **Delete** restores all defaults.

Changes apply immediately and save for all projects. Every newly opened Stvena
window loads the same shortcuts. Layout settings remain specific to each project.
Existing repository shortcuts migrate automatically when you open the first
project with custom bindings; shared settings take precedence after that,
including when you reset to defaults.

The controls screen, Actions menu and clickable toolbars show your configured
keys. A shortcut retains its context-dependent behavior (for example, **d**
scrolls code but removes an attachment in Context). **?**, **j/k**, named
navigation keys (arrows, Enter, Esc, Tab, Backspace, etc.) and **F6** stay fixed.
Configured global shortcuts are intercepted in either pane. Freed shortcuts and
ordinary text entry pass through to the agent.
The table below lists defaults.

| Key | Action |
| --- | --- |
| F6 | Focus the bottom panel; arrows navigate, Enter activates, Esc returns |
| Ctrl-G | Switch panes (agent / workspace), preserving the open file and selection |
| Ctrl-] | Choose Codex, Claude Code, or the launch command for a new terminal |
| Ctrl-N / Ctrl-P | Switch to the next / previous agent terminal |
| Ctrl-W | Close current agent terminal and stop its command |
| Ctrl-Q | Close stvena and stop all running agents from either pane |
| Ctrl-C | Native interrupt while the agent has focus |
| a | Open searchable-by-shortcut Actions menu |
| ↑/↓, j/k | Select file or move through code |
| Enter / f / Backspace | Open file / return to browser |
| v / s / w | Full file / side-by-side diff / wrap lines |
| F / + / - | Fullscreen review / wider review / wider agent |
| 1 / 2 / 3 / 4 / Tab | Session / Workspace / Project files / Branch changes / workspace scope |
| / | Filter paths in browser; search text in code |
| m / M / : | Next / previous text match / go to source line |
| n / p | Next / previous file |
| N | Open the next unreviewed file in the current view |
| I / L | Toggle Review inbox / open observed session timeline |
| d/u, PageDown/PageUp | Move half a page |
| g/G, Home/End | First / last file or source line |
| [ / ] | Previous / next hunk or full-file change |
| h/l, ←/→ / 0 | Horizontal scroll / reset horizontal offset |
| P / R | Pin or resume live / compare since review |
| K / Z | Start Review checkpoint / finish and preview the assembled draft |
| Space / H | Mark file / hunk reviewed |
| b | Paste selected code, or the assembled draft while in Context |
| x / B | Collect selected code / open the saved context tray |
| V / y / Y / e | Select range / copy code / copy path / open editor |
| c / C / E | Add comment / view comments / copy feedback |
| t / T | Run checks / view results |
| S / A | Stage or unstage file / hunk, with confirmation |
| ? / Esc | Help / close current overlay or return to files |
| q | Quit after all agents exit, or quit standalone review |

Review shortcuts are dimmed while the agent has focus. Agent input passes through
unchanged except for F6, configured global shortcuts (by default Ctrl-G, Ctrl-],
Ctrl-N, Ctrl-P, Ctrl-W and Ctrl-Q), and their active Command aliases. Footer
navigation consumes arrows and Enter only while the panel has focus.
Shortcuts inside a bracketed paste are treated as literal pasted text.

### Command keys in your terminal

Stvena cannot override keyboard shortcuts handled by the terminal application.
For example, Apple Terminal uses **Cmd-G** for Find Next and **Cmd-W** to close
its tab ([Apple's shortcut reference](https://support.apple.com/guide/terminal/keyboard-shortcuts-trmlshtcts/mac)).
Use **Ctrl-G** to switch Stvena panes and **Ctrl-]** to open its agent chooser,
or click the footer controls. Stvena displays your configured shortcuts.

Optional Command aliases are recognized when a terminal forwards them using the
[Kitty keyboard protocol's CSI-u encoding](https://sw.kovidgoyal.net/kitty/keyboard-protocol/).
If your terminal supports custom Command bindings that send escape sequences,
you can configure these aliases there while the corresponding Ctrl chord is
assigned in Stvena. Reassigning a Ctrl chord also releases its Command alias. This requires terminal-side support;
changing a Stvena binding or enabling a keyboard protocol cannot take over a
terminal application's menu shortcut.

| Command key | Sequence to send (`ESC` is the escape byte) |
| --- | --- |
| Cmd-G | `ESC[103;9u` |
| Cmd-] | `ESC[93;9u` |
| Cmd-N | `ESC[110;9u` |
| Cmd-P | `ESC[112;9u` |
| Cmd-W | `ESC[119;9u` |
| Cmd-Q | `ESC[113;9u` |

**Ctrl-C** remains the agent's native interrupt. Ordinary review letter keys
continue to work without a modifier.

## Local storage and limits

Session metadata, pinned checkpoints, comments, review marks, context attachments, draft requests,
layout preferences and the latest check result
are stored under the OS user cache directory in `stvena/<repository hash>`.
Shared shortcuts are stored separately at `stvena/hotkeys.json` under the OS
user configuration directory: `~/Library/Application Support` on macOS, and
`$XDG_CONFIG_HOME` (or `~/.config`) on Linux. Closing an unedited window does not
overwrite shortcut changes made in another project. Reopen an already running
window to load changes made elsewhere.

Private `refs/stvena/sessions/*` and `refs/stvena/reviews/*` retain Git objects
needed for snapshots and saved review references. Snapshot capture uses a
separate temporary index, preserving your normal index and branch. Snapshots
contain Git-normalized, nonignored working-copy content, including tracked
ignored paths. They are not a raw filesystem backup. Retained objects do not
currently expire automatically.

Use one stvena process per repository for saved review state; simultaneous
writers to its review JSON are not merged. Sessions retain their launch baseline;
reopening one extends its live comparison against the current working copy.

Refreshes run approximately every 700 ms plus collection time. Git preview
commands have a five-second timeout and 16 MiB output limit; snapshot commands
have a 15-second timeout. A changing workspace is retried on the next refresh.
Full files load asynchronously up to 16 MiB. Very large previews are labeled
incomplete. Workspace untracked previews load the first 100 files, up to 256 KiB
and 400 lines each; all discovered paths remain listed. Repository and check
output control characters are escaped before review rendering.

## Development

- `internal/app`: PTY lifecycle, event loop, actions, mouse and keyboard routing.
- `internal/diffview`: bounded Git collection, immutable content and staging.
- `internal/session`: private-index capture, retained refs and session storage.
- `internal/review`: navigation, line anchors, persistent marks and feedback.
- `internal/checks`: captured-code execution, bounded logs and cancellation.
- `internal/ui`: responsive panes, code colors, comparisons and action overlays.

```sh
go test ./...
go test -race ./...
go vet ./...
```

See [competitive research and roadmap](competitive-research.md) for the
research behind these modules and the remaining larger opportunities.

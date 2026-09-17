# Changelog

## 0.4.1

- Find a project without Git as soon as Stvena starts in it, instead of up to
  five seconds later. Until then the extension could not see the project, so
  the agent's first reads and edits were not followed. A folder that is not yet
  resolved is looked up again whenever Stvena's bridge registry changes, which
  needs no git process.

## 0.4.0

- Follow agent edits and reviews in projects that are not Git repositories.
  Stvena keeps the captures and the bridge descriptors in its own directory
  outside your source tree, and the extension finds them through Stvena's
  bridge registry when Git has nothing to say about the folder.
- Resolve Git repositories exactly as before: Git is always asked first, and a
  repository's descriptors stay in its Git directory.
- Adopt a project's new descriptor location when it gains a Git repository,
  instead of polling the old one until the window is reloaded.

## 0.3.1

- Reject newly added files as a whole from their inline action, while validating
  the captured hunk token before queuing the deletion.
- Make Undo on a whole-file rejection work from every affected change block,
  and label it “Undo file rejection.”
- Report the actual outcome of Apply Rejections, including failed reverts and
  warnings about changes left in the staging index.

## 0.3.0

- Accept or reject each of the agent's change blocks from the working file, with
  Accept, Reject and Reject with reason above every block.
- Tint change blocks by state and badge files in the Explorer with how many
  blocks still need review.
- Queue rejections and report what they are waiting for: a rejection reverts the
  lines when the agent finishes its turn, never underneath a working agent.
- Roll back and explain a decision Stvena refuses, such as a block the agent
  changed again after the editor drew it.
- Gate the new actions on what the running Stvena advertises, so an older binary
  keeps the 0.2.x behaviour instead of failing.
- Add `stvena.showCodeLens` to keep the tinting without the buttons.
- Ask the agent about a selection with Cmd-K Cmd-A: the question and the
  captured code go into the agent's input, unsubmitted.
- Announce the editor to Stvena so it can offer IDE mode, where the agent gets
  the whole integrated terminal and review happens here.

## 0.2.2

- Follow the source range currently selected in Stvena without opening a native
  editor diff.
- Send an editor line to TUI review or add a saved editor selection to Stvena's
  captured context tray.
- Show cumulative unreviewed-file counts in the status bar.

## 0.2.1

- Add the Stvena icon to the extension listing.

## 0.2.0

First GitHub preview, paired with Stvena v0.2.0-preview.1.

- Follow saved edits in VS Code and Antigravity while terminal focus stays put.
- Show reported agent reads with blue markers and captured edits with amber markers.
- Pause/resume following and inspect the latest activity from Explorer.
- Preserve unsaved buffers and support nested Git folders and worktrees.

Install the binary and VSIX from the same preview release. Other editor forks,
remote workspaces, and live model-driven read hook execution remain unverified.

# Stvena: competitive research and proposed roadmap

Research date: 7 September 2026. Scope: official product documentation and repositories, compared with the current local stvena implementation. This is a documentation comparison, not a hands-on benchmark of competitors. Features described as opportunities are proposals; absence from sampled documentation does not establish that a competitor lacks them. No application features were changed for this research.

**Recommendation:** Focus stvena on reviewing an active coding session: what changed, what was reviewed, what changed again, and which checks apply to the current code. Its existing single-command, agent-independent workflow is a useful starting point. Broader competitors already provide substantial agent orchestration and Git management.

## Competitive landscape

| Tool | Documented capabilities relevant to us | Gaps in current stvena |
| --- | --- | --- |
| [Sidecar](https://github.com/marcus/sidecar) | Embedded agent terminals, worktrees, resizable panes, mouse support, syntax-highlighted unified/side-by-side diffs, file tree, Git actions and session overview. Closest direct competitor. | Syntax highlighting, side-by-side comparison, mouse navigation, adjustable panes, worktrees and multiple sessions. |
| [Lazygit](https://github.com/jesseduffield/lazygit) | Individual-line/hunk staging, worktrees, commit comparison, history editing and reflog-based undo. | Selective staging, commit history and reference comparisons. Reflog undo is not a general undo for uncommitted file edits. |
| [Delta](https://github.com/dandavison/delta) | Syntax and word-level highlighting, side-by-side wrapping, moved-code coloring, improved conflict display and theme support. A rendering benchmark rather than an agent workspace. | All of those presentation features except ordinary colored additions/deletions and line numbers. |
| [Difftastic](https://github.com/Wilfred/difftastic) | Compares source using syntax structure. | Structural comparison; stvena currently presents Git's line-based patches. |
| [tuicr](https://github.com/agavra/tuicr/blob/main/README.md) | Line/range/file comments, persistent file/hunk review tracking, commit-range review, and review export to hosting services or an agent-ready clipboard block. | Persistent reviews, hunk-level tracking, inline feedback and export. |
| [Conductor diff viewer](https://www.conductor.build/docs/reference/diff-viewer) and [checkpoints](https://www.conductor.build/docs/reference/checkpoints) | Changed-line feedback to the agent, commit-filtered review and automatic turn checkpoints with restore. | Comments, commit/turn views and snapshots. Checkpoints alone would be parity. |
| [Superset](https://docs.superset.sh/overview) | Worktree-based parallel agent workspaces and integrated development tools. Its [diff viewer](https://docs.superset.sh/diff-viewer) includes Git actions, review comments and PR check status. | Workspaces, parallel sessions, Git/PR operations and check status. |
| [cmux](https://cmux.com/) | Native macOS terminal using Ghostty, split panes, attention notifications, programmable interfaces, browser and session restoration. | Attention signals, session restoration, configurable panes and mature terminal interaction. |
| [GitButler](https://docs.gitbutler.com/ai-agents/review-agent-work) | TUI, CLI and desktop inspection of agent-created branches/commits, with operation history and recovery. Its [agent overview](https://docs.gitbutler.com/ai-agents/overview) describes organizing work into parallel/stacked branches. | Branch/task organization and history/recovery tools. |

Sidecar's [Git documentation](https://sidecar.haplab.com/docs/git-plugin) also describes selective updates, filesystem watching and persisted layout preferences. These are practical implementation references for improving our periodic collector and fixed layout.

## What stvena already has

The local code and README establish the following baseline:

- One interactive child command with a live adjacent Git review pane.
- Separate staged, unstaged and untracked records; unique path count and per-scope line totals.
- File selection, path filtering, numbered patches, hunk navigation and horizontal scrolling.
- Full-file inspection of the appropriate worktree/index/pre-deletion version.
- RGB and indexed terminal color rendering, responsive stacking and a persistent controls bar.
- In-memory file review marks invalidated when the observed change changes.
- Explicit binary/large-preview handling, bounded Git commands and asynchronous full-file loading.

These are useful foundations. Agent independence, terminal operation, full-file views and live diffs are already offered elsewhere and should not be marketed as exclusive features.

The implementation also exposes several concrete limitations:

- The entire application returns when the child command exits, so review ends with the agent process.
- Collection covers current index/worktree changes. A commit can make a session's completed edits disappear from the current-change list.
- Pre-existing local changes are mixed into the initial view; there is no session baseline.
- Review marks are per-file and disappear across launches.
- Filtering searches paths, not source text. There is no go-to-line, symbol navigation, clipboard action or editor jump.
- Patch/full-file toggling resets the viewport rather than preserving the corresponding source location.
- Pane proportions and keybindings are fixed; there is no review mouse handling or exposed agent scrollback navigation.
- Git data is recollected periodically, and numbered display lines are regenerated rather than cached by content version.

## Recommended implementation order

Relative effort below is an engineering judgment from this codebase, not a delivery commitment. Small means a contained UI/flow change; medium crosses collection, state and rendering; large introduces durable snapshots, integrations or mutating operations.

| Order | Module / improvement | First useful scope | Effort |
| --- | --- | --- | --- |
| 1 | Review usability | Stay open after agent exit; fullscreen review; adjustable pane width; source search; go-to-line; copy path/selected text; preserve source position across view changes. | Small–medium per item |
| 2 | Diff readability | Syntax highlighting in both views; word-level highlights; soft wrapping; optional side-by-side mode; expandable unchanged context. | Medium |
| 3 | Session comparison | Capture launch state; distinguish pre-existing work from changes observed during this run; retain session changes after commits; reopen a saved session for review. | Large |
| 4 | Persistent review and feedback | Save file/hunk review state; attach line/range comments to a code version; export a structured feedback block for the existing agent. | Medium–large |
| 5 | Version-specific validation | Explicit test/lint commands with exit status, logs, timestamps and the snapshot they tested; indicate when results become stale. | Large |
| 6 | Optional Git actions | Stage/unstage a file first, then hunks; verify the selected patch has not changed before applying. Add restore only after snapshot support. | Medium–large |
| 7 | Workspaces and agent awareness | Worktree switching, additional agents, attention notifications and resume support. | Large |

A sensible first release would ship items 1 and 2, followed by session comparison. Workspaces should follow demonstrated demand for parallel agents. An embedded browser, cloud scheduler or full Git history editor would substantially expand the project before its core review loop is mature.

## Opportunities to differentiate

**1. A clear distinction between pre-existing work and this session's changes.**

Example: a repository starts with twelve dirty files. An agent then edits two and creates a third. The default session view should show the changes since launch, while a separate workspace view keeps all pre-existing work accessible. Session changes should remain visible even if the agent commits them.

Capture baseline bytes and index state before starting the child. A HEAD hash alone cannot represent pre-existing uncommitted or untracked files. Start with local snapshots and manual checkpoints; automatic turn boundaries can follow supported agent integrations.

Conductor already has [turn checkpoints](https://www.conductor.build/docs/reference/checkpoints). The opportunity is a particularly clear, agent-independent terminal workflow around the baseline, rather than claiming snapshots are new. Filesystem observation establishes when a change was seen, not who made it; edits by another terminal or editor must not automatically be attributed to the agent.

**2. Show exactly what changed after a human reviewed it.**

Our current file fingerprint clears the whole file's review marker. A stronger workflow would retain the reviewed version, preserve unaffected reviewed hunks, and offer a direct comparison against that version. The user should see a queue such as “two reviewed areas changed again” and jump straight to them.

tuicr already documents persistent [hunk-level tracking and review-aware commit selection](https://github.com/agavra/tuicr/blob/main/README.md). Our opportunity would be the experience of continuously reviewing evolving, uncommitted agent output. Reliable anchors need content context and version identifiers, not only line numbers.

**3. Keep the code being read stable while the agent continues.**

Provide separate Live and Pinned views. Pinning freezes the displayed version, not the agent process. Incoming edits produce a visible update count; the reviewer chooses when to advance or compare versions. Comments and review markers always refer to the pinned code.

This would address a problem inherent in a live review pane: source lines can move underneath the reader. I did not establish whether every competitor offers an equivalent mode; treat it as a candidate workflow to test with users.

**4. Tie test results to the version actually tested.**

Example: tests pass on snapshot A, then the agent changes a dependency or source file. The UI should report “passed on an earlier version; current code unverified,” rather than retain a generic green status.

Store the command, working directory, exit code, logs and source snapshot. Run against an immutable snapshot/worktree when possible. Comparing hashes before and after a test is a weaker alternative and must report concurrent edits as ambiguous. A code hash alone cannot prove the environment, external services or test data are identical.

Superset already displays [PR check status](https://docs.superset.sh/diff-viewer). The proposed difference is explicit local-code freshness during an active session. This is the strongest differentiation hypothesis from the research, not a verified market-exclusive capability.

**5. Turn a selected code range into precise feedback.**

Let the user select a range, add a comment, preview the resulting message and copy it to their existing CLI. Include path, version, relevant code and the requested correction. If the code changed before handoff, mark the comment as outdated and show the new version.

Inline feedback already exists in [Conductor](https://www.conductor.build/docs/reference/diff-viewer) and [tuicr](https://github.com/agavra/tuicr/blob/main/README.md). It remains a high-value missing part of our workflow. Begin with local drafts and clipboard export; sending into a live agent should be an explicit action and use an appropriate integration.

## Implementation implications

Extend `internal/ui` and `internal/review` for source search, view sizing and position mapping. Cache rendered/numbered content by selected version. Extend `internal/diffview` with explicit comparison bases; add a focused `internal/session` package when persistence is implemented. Feedback and check records should share those same version identifiers.

Keep Git patches authoritative for counts, line anchors and any future staging. Delta or Difftastic can inform or supply an optional presentation mode, but their decorated output should not become the parser for Git mutations.

For agent state, use supported hooks/adapters with an unknown-state fallback. Superset's [status documentation](https://docs.superset.sh/agent-status) explicitly notes that available lifecycle signals vary by agent. Terminal text matching alone should not be presented as reliable knowledge that an agent has finished or passed tests.

Continue terminal regression coverage for colors, Unicode, input routing, paste, resize and shutdown. Add a standalone review mode so people can use the viewer beside an already running CLI or in tmux. Validate macOS/Linux and remote-terminal behavior explicitly before making broad platform claims.

## Product validation

Evaluate proposed features on representative workflows rather than counting features:

1. Open a dirty repository, run a task, commit its result and locate only changes made since launch.
2. Review a hunk, let the agent modify it again and find the new changes without rereading the file.
3. Pin a version, add feedback while the agent writes, and verify the comment still identifies the intended code.
4. Run tests, edit code afterward and confirm the UI stops treating the current version as tested.
5. Exit the agent and finish review without losing position or progress.

Observe task completion, navigation mistakes, repeated reading and whether users can correctly explain which version was reviewed/tested. Those observations should decide whether more orchestration is useful.

## Implementation update — 7 September 2026

The original comparison above describes the pre-implementation baseline. The
following connected modules are now implemented locally:

- **Usability:** review survives agent exit; standalone review, Actions menu,
  bottom controls, review mouse handling, fullscreen/adjustable panes, persisted
  display preferences, source search, go-to-line, clipboard and GUI editor jumps.
- **Readability:** syntax-colored unified/full-file views, optional paired diffs
  with changed character spans emphasized, wrapping and source position mapping.
  Complete-file mode provides unchanged context.
- **Session comparison:** capture before CLI startup, separate Session/Workspace
  views, comparison that survives commits, saved session IDs and baseline reopen.
  Captures use a private index and Git filters; they are Git-normalized snapshots,
  not byte-for-byte filesystem backups or process-attribution records.
- **Review feedback:** persistent file/hunk marks, prior-version comparison,
  stable pinned views, line/range comments, version-aware stale feedback and
  clipboard export. A changed file with review history gets a revisit marker.
- **Validation:** explicitly submitted commands in temporary captured-code
  checkouts, saved exit codes/logs/timestamps and current-tree freshness. Changed
  existing source files during a check are reported. Environment/dependency
  reproducibility and generated files are outside that provenance guarantee.
- **Git actions:** confirmed file/hunk staging and unstaging, stale-view rejection,
  and Git patch validation. No working-copy discard or restore is exposed.

Still proposed: structural/moved-code diff presentation, expandable context
within individual hunks, arbitrary line staging, tracked binary staging, restore
with recovery UI, manual/automatic turn checkpoints, comment editing/resolution,
worktree and multi-agent orchestration, agent scrollback, remote PR integration,
configurable keymaps and snapshot retention management. Reopening a saved session
continues comparison from its baseline; it does not resume the agent process.

The README documents supported workflows and limits. Automated tests cover dirty
and unborn repositories, preservation of the index, changes after commits,
immutable content, pinning and persisted feedback, selective staging, versioned
checks/cancellation, terminal colors and responsive rendering. A real macOS PTY
smoke run also exercised agent exit, full-file/paired views, Actions, snapshot
checks, stale status, live/pinned switching and standalone session reopen. Linux
and remote-terminal interaction have not received the same hands-on validation.

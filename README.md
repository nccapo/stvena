<p align="center">
  <img src="extensions/vscode/media/icon.png" alt="Stvena icon" width="128" height="128">
</p>

# Stvena

**Stop reopening your IDE just to review what your coding agent changed.**

Run Codex or Claude Code beside a live review workspace in the same terminal.
Inspect every changed file, read the surrounding code, run checks, select exact
lines, and send that context straight back to the agent.

<!-- demo:start -->
https://github.com/user-attachments/assets/7aecafe5-1e45-442e-ab49-82cfc3750d9f
<!-- demo:end -->

[![CI](https://github.com/nccapo/stvena/actions/workflows/ci.yml/badge.svg)](https://github.com/nccapo/stvena/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/nccapo/stvena)](https://github.com/nccapo/stvena/releases/latest)
[![VS Code Marketplace](https://img.shields.io/visual-studio-marketplace/v/nccapo.stvena-live?label=VS%20Code)](https://marketplace.visualstudio.com/items?itemName=nccapo.stvena-live)

Built in Go. Designed for terminal workflows with Codex and Claude Code.
**Early-stage software:** interfaces and local storage formats may change.

If Stvena saves you from the "open the IDE just for the diff" loop, consider
[starring the repository](https://github.com/nccapo/stvena).

## Why Stvena?

- **See the work as it happens.** Review changes since the agent started, or
  inspect staged, unstaged, and new files across the workspace.
- **Read beyond the diff.** Browse the project tree, open complete files, and
  switch between unified and side-by-side views with syntax highlighting.
- **Give the agent precise context.** Select code with the mouse or keyboard,
  collect snippets across files, and paste an assembled draft into the agent.
  You decide when to submit it.
- **Run multiple agents at once.** Keep independent Codex and Claude Code
  terminals running, switch between them, and review their combined changes in
  one workspace.
- **Follow saved edits in your editor.** The experimental Stvena Live extension
  lists each newly captured batch and opens the working source at its first
  changed line while focus stays in the integrated terminal.
- **Keep track of your review.** Pin a captured version, mark files and hunks
  reviewed, and see when a reviewed change has changed again.
- **Check the code you are looking at.** Run a command in a temporary checkout
  of a captured version, inspect logs, and jump from recognized failures to source.
- **Stage deliberately.** Stage or unstage files and hunks with confirmation.

## Installation

You need **Git**, an interactive terminal, and your chosen agent CLI installed
and authenticated. Stvena uses Unix PTYs and process groups; macOS and Linux are
the intended platforms. Native Windows is not supported.

### Homebrew (macOS and Linux)

With Homebrew installed, run:

```sh
brew install nccapo/stvena/stvena
stvena --version
```

This taps [nccapo/homebrew-stvena](https://github.com/nccapo/homebrew-stvena),
trusts only the `stvena` formula, and installs a prebuilt binary verified against
the release's SHA-256 checksum. No Go installation is required. The formula
supports Apple Silicon and Intel Macs, plus ARM64 and amd64 Linux systems.
Install and authenticate Codex or Claude Code separately before launching Stvena.

The tap tracks stable releases by default. To upgrade or uninstall:

```sh
brew upgrade stvena
brew uninstall stvena
```

Preview releases are not installed automatically. For the editor integration
preview, follow [Follow reads and edits in VS Code](#follow-reads-and-edits-in-vs-code).

If you previously used `install.sh` and Homebrew reports an existing binary in
`/opt/homebrew/bin` or `/usr/local/bin`, locate it first:

```sh
command -v stvena
```

Remove that manually installed Stvena binary if it occupies Homebrew's target
path, then rerun `brew install nccapo/stvena/stvena`. See the
[tap documentation](https://github.com/nccapo/homebrew-stvena#coming-from-installsh)
for migration details.

### Install script (macOS and Linux)

No Go installation is required:

```sh
curl -fsSL https://raw.githubusercontent.com/nccapo/stvena/main/install.sh | sh
```

The installer downloads the correct macOS or Linux release for your machine,
verifies it against the published SHA-256 checksums, and installs it in an
existing writable binary directory or `$HOME/.local/bin`.

| Operating system | Architectures |
| --- | --- |
| macOS | Intel (`amd64`), Apple Silicon (`arm64`) |
| Linux | `amd64`, `arm64` |

Run the same command again to upgrade to the latest release. Check the installed
version with `stvena --version`.

To choose the installation directory or install a specific release:

```sh
curl -fsSL https://raw.githubusercontent.com/nccapo/stvena/main/install.sh | \
  STVENA_INSTALL_DIR="$HOME/.local/bin" STVENA_VERSION=v0.1.0 sh
```

### Install with Go or build from source

These options require Go 1.24.2 or newer:

```sh
go install github.com/nccapo/stvena/cmd/stvena@latest

git clone https://github.com/nccapo/stvena.git
cd stvena
go build -o bin/stvena ./cmd/stvena
./bin/stvena --help
```

## Quick start

Launch Stvena **inside the Git project you want to work on**. You can pass an
agent command and its arguments after `--`, or reopen a saved review:

```sh
cd /path/to/your/project
stvena                    # Starts Codex
stvena claude             # Starts Claude Code
stvena -- codex --model YOUR_MODEL
stvena review
stvena sessions
stvena review --session ID
```

**Shortcuts use Ctrl on both macOS and Linux.** Command keys normally belong
to the terminal application. Optional Command aliases require a terminal that
forwards them; see [terminal shortcut setup](docs/usage.md#command-keys-in-your-terminal).

**Multiple agent terminals:** Ctrl-] opens a chooser: **c** starts Codex, **l**
starts Claude Code, and **Enter** repeats your original command with its arguments.
Ctrl-N / Ctrl-P switch between terminals while background agents keep running.
See [Work with multiple agents](#work-with-multiple-agents) for the complete
workflow.

Reopening a review compares a saved baseline with today's workspace; it does
not resume an agent conversation. Other commands can run in the agent pane,
but direct selection paste is supported for Codex and Claude Code.

## A typical workflow

1. Start `stvena` in your project. Press **Enter** to configure shortcuts, or
   **Esc** to type your task in the agent.
2. Press **Ctrl-G** to focus review. Open a changed file, or press **3** to browse
   the project. Press **v** to switch between a diff and the full file.
3. Drag over code and press **b** to paste it into the agent's input. Add your
   request and submit it in the CLI. Use **x** to collect multiple selections
   and **B** to open the context tray.
4. Press **t** to run a check such as `go test ./...`. Open results with **T**,
   then **o** for recognized problem locations.
5. Mark a file reviewed with **Space** and move to the next unreviewed file with
   **N**. In Workspace view, **S** stages or unstages the selected file.

| Key | Action |
| --- | --- |
| **F6** | Focus bottom panel; arrows select, Enter activates, Esc returns |
| **Ctrl-G** | Switch between agent and review |
| **Ctrl-]** | Open another agent terminal |
| **Ctrl-N / Ctrl-P** | Next / previous agent terminal |
| **Ctrl-Y** | Next attention: error, review, waiting, changed, running, done |
| **Ctrl-W** | Close current agent terminal and stop its command |
| **Ctrl-Q** | Quit Stvena and stop all agents |
| **1 / 2 / 3** | Session changes / workspace changes / project files |
| **Enter / f** | Open file / return to file browser |
| **v** | Toggle diff and full-file view |
| **Drag / V** | Select code with mouse / keyboard |
| **b / x / B** | Paste selection / collect selection / open context |
| **P** | Pin the displayed version or resume live updates |
| **K / Z** | Start Review checkpoint / finish and preview its draft |
| **Space / N** | Mark file reviewed / next unreviewed file |
| **t / T** | Run a check / inspect results |
| **a / ?** | Actions menu / keyboard help |

Stvena opens with the bottom panel focused and **Configuration** selected.
Press **Enter** to change shortcuts, or **Esc** to start typing in the agent.
Use arrows to select any other footer action. For a Ctrl-G conflict, change
**Switch panes** to **Ctrl-O**; Ctrl-G then reaches the agent. Settings apply
across projects. **?** opens Configuration in review; **F6** can refocus the footer.
Review shortcuts apply when review has focus. **Ctrl-C** keeps the agent's
native interrupt behavior. Review remains open after the agent exits; **q**
closes it. See the [complete user guide](docs/usage.md) for comments, hunk staging,
search, clipboard tools, and every shortcut.

For a review pause, switch panes with **Ctrl-G**, then choose **Actions → Review
checkpoint** (**K**) after the agent changes files. Stvena pins the captured
session tree. Use **Space/H** to review files/hunks, **N** for the next unreviewed
file, and **c** or **x** to save exact-line comments or selections across files.
**Finish checkpoint** (**Z**) warns about remaining work and previews one draft;
**b** pastes it into Codex/Claude Code without submitting, or copies it in
standalone review. **P** resumes live; changed items need review again. An open
checkpoint and its feedback survive reopening a saved review.

## Work with multiple agents

Stvena can run several agent terminals in one session. Each terminal keeps its
own screen, conversation, and unfinished input. The agent pane shows one terminal
at a time, and the header identifies the active command and its position, such as
`codex 1/2`. Other agents continue running in the background.

Press **Ctrl-]** from either pane to open the new-agent chooser:

- **c** starts Codex with its CLI defaults.
- **l** starts Claude Code with its CLI defaults.
- **Enter** starts the selected option. The initial option repeats the command
  used to launch Stvena, including its arguments.

Every new terminal starts in the original working directory. All agents share
the same session baseline and review workspace, so changes from every agent
appear together. Code selections and review drafts are pasted into the currently
selected agent.

Use **Ctrl-N** and **Ctrl-P** to move between agent terminals. **Ctrl-W** stops
and closes the selected agent; finished terminals otherwise remain available for
inspection. Closing the last agent leaves the review workspace open, where
**Ctrl-]** can start another. **Ctrl-Q** stops every agent and exits Stvena.

Agent terminals are live processes rather than saved conversations. Reopening a
saved review does not restart them, and multiple-agent shortcuts are unavailable
in standalone `stvena review` mode.

## Agent attention

A compact strip above the panes shows every terminal's stable name and status:
`● RUNNING`, `! REVIEW`, `? WAITING`, `+ CHANGED`, `✓ DONE`, `× ERROR`, or
`· IDLE`. Brackets identify the active terminal; `→` identifies the highest
attention priority. Color reinforces these text labels. `3f` always means three
unreviewed file paths (a rename can include both paths). `/ RUN` means the agent is still working while it needs review.
The header counts agents, running agents, agents with unreviewed files, waiting
agents, and errors. The strip uses at most two rows; overflow keeps the highest
priority agent visible and shows how many other agents are hidden.

**Ctrl-Y** (Next attention) starts at the highest priority and cycles through
error → review → waiting → changed → running → done. Normal terminal navigation
resets the cycle. Configure it in **? → Configuration**, like the other global
shortcuts; the footer also provides Next attention. Command-Y is an optional
alias when the terminal forwards it. No background event switches panes.

Selecting attention opens the relevant file in **This session** when possible.
An existing pinned checkpoint or unfinished review input stays intact. The
normal **e** editor action then opens that file; the live editor bridge continues
to publish combined workspace changes.

**REVIEW clears only after Space marks the captured file reviewed, or H marks
all its hunks reviewed, in This session or a session checkpoint.** Opening the
agent or diff does not acknowledge it. Reviewing an older pinned version does
not clear newer changes. An acknowledged session stays clear when another agent
later edits the same path. New edits by the original agent create new review
items. If edits are reverted before capture, Next attention offers an explicit
no-net-change acknowledgement. Closing a terminal removes its live indicators;
its changes and existing review records remain in the shared workspace.

Execution, review, activity time and changed-file ownership are separate state.
Attention priority is derived from them. Invocation-local observers use
[Claude hooks](https://code.claude.com/docs/en/hooks) and
[Codex hooks](https://developers.openai.com/codex/hooks): prompt/tool events mark
running, Stop marks a completed turn, Claude StopFailure and unsuccessful process
exits mark error. Claude permission/idle notifications provide waiting signals.
Without structured hooks, PTY output means running; three seconds without output
means idle, **never** waiting or completed. A successful process exit means done.

File attribution compares named files before and after Claude Edit/Write/MultiEdit
or Codex apply_patch calls, including additions, deletions and renames. The git
snapshot watcher then supplies captured review versions. Arbitrary shell/MCP
writes and commands without hooks cannot be reliably attributed in a shared
working directory; they remain visible in the combined diff without assigning
an owner. Concurrent edits to the same file are reviewed against its combined
captured diff. Git-ignored files are outside the normal diff capture.

Codex must support and trust the configured hooks. Explicit Claude `--settings`
are preserved, so automatic observers are not added in that case. Codex waiting
prompts and CLI turn failures without a supported event cannot be detected
reliably; Stvena does not infer them from terminal text. Live attention state is
not restored after restarting Stvena; saved review records continue to work.

## Follow reads and edits in VS Code

The experimental [Stvena Live extension](https://marketplace.visualstudio.com/items?itemName=nccapo.stvena-live) follows
captured edits in the editor while Stvena runs in its integrated terminal. It
opens working source files at the changed line, lists the latest changed files,
and lets you pause and resume following. Supported Codex and Claude tool hooks
also report read locations. Blue read markers and amber edit markers show the
relevant lines with an inline label; markers expire after 15 seconds. Codex
requires its normal `/hooks` trust review before read reporting runs.

Install **Stvena Live** by **nccapo** from the VS Code Extensions view, or run:

```sh
code --install-extension nccapo.stvena-live
```

Then install the compatible Stvena binary on macOS or Linux. The extension and
terminal application are installed separately; editor following requires the
[v0.2.0-preview.1 binary](https://github.com/nccapo/stvena/releases/tag/v0.2.0-preview.1)
or a current source build:

```sh
curl -fsSL https://raw.githubusercontent.com/nccapo/stvena/v0.2.0-preview.1/install.sh | \
  STVENA_VERSION=v0.2.0-preview.1 sh
```

Open a trusted local Git workspace in VS Code, check that `stvena --version`
reports `0.2.0-preview.1`, and run `stvena` or `stvena claude` in its integrated
terminal. No Go or Node.js installation is needed. The default Stvena installer
selects the stable release, which predates the editor bridge, so use the explicit
preview version above.

Stvena Live **0.2.1** includes the Stvena icon in its extension listing. Marketplace
installations receive updates through VS Code according to your update settings.
For Antigravity or manual installation, use **Extensions: Install from VSIX…**;
the original GitHub preview includes extension **0.2.0**, and a current source
build produces **0.2.1**. See the [extension guide](extensions/vscode/README.md)
for both installation paths and troubleshooting.

Click **Stvena: Following** in the status bar to pause or resume navigation;
**Stvena: Show Latest Activity** returns to the latest reported read or edit.
If the view stays **Waiting**, check the binary version and workspace trust,
and open **Stvena Live** in the Output panel for connection errors.

The extension targets VS Code 1.85 or newer. It reads saved
file captures from the repository's Git directory; it does not run commands,
write source, add telemetry, or expose a network service.

Updates are snapshots sampled roughly every 700 ms plus capture and extension
polling time. They include saved edits from every workspace writer, without
per-agent attribution or guaranteed intermediate history. Automatic navigation
skips deleted files and files with unsaved editor changes.

The first target is the VS Code extension API. Zed requires a separate
integration. See the extension guide for installation, compatibility, and
capture limits, or the [bridge protocol](docs/editor-integration.md) to build
another local editor integration.

## Privacy and local data

Stvena does not require its own API key or account. It launches the agent CLI
you already use with your environment and credentials. Your chosen agent's
network access and data handling still apply.

- Review metadata, comments, snippets, draft requests, layout preferences, and check
  output are stored under your OS cache directory in `stvena/<repository hash>`.
- Custom shortcuts are shared across projects in your OS user configuration
  directory at `stvena/hotkeys.json`.
- Captured code is retained in local Git objects under `refs/stvena/sessions/*`
  and `refs/stvena/reviews/*`. Use normal branch pushes; a mirror push can also
  publish these private refs.
- Capture respects Git ignore rules for untracked files. **Already tracked
  files are captured even if subsequently ignored.** Put credentials outside
  Git and inspect your index before committing.
- Selected code is pasted only when requested; Stvena does not press Enter to
  submit it. Drafts can include absolute paths and captured source.
- Check commands run with your user permissions and inherited environment.
  **The temporary checkout is not a sandbox.** Run commands you trust.

Retained snapshots do not expire automatically. Use one Stvena process per
repository to avoid competing writes to saved review state. The
[storage and limits guide](docs/usage.md#local-storage-and-limits) describes
capture behavior and size limits.

## Development and contributions

Bug reports, focused pull requests, and usability feedback are welcome.
Read [CONTRIBUTING.md](CONTRIBUTING.md) for setup, checks, and what to include in
a report. For sensitive findings, see [SECURITY.md](SECURITY.md).

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./...

cd extensions/vscode
npm ci
npm test
npm run package
```

Maintainers publish the prebuilt archives and checksums by pushing a semantic
version tag. See [Publish a release](CONTRIBUTING.md#publish-a-release) for the
release command and verification steps.

| Package | Responsibility |
| --- | --- |
| `cmd/stvena` | Command entry point |
| `internal/app` | Agent lifecycle, event loop, input, and actions |
| `internal/diffview` | Git collection, captured content, and staging |
| `internal/session` | Snapshot capture, retained refs, and session storage |
| `internal/review` | Navigation, selections, review marks, and feedback |
| `internal/checks` | Check execution, bounded logs, and problem locations |
| `internal/editor` | Local editor bridge and captured-change protocol |
| `internal/ui` | Terminal layout, code rendering, and controls |
| `extensions/vscode` | Stvena Live editor navigation and latest-change view |

The [product direction](docs/product-direction.md) and
[research notes](docs/competitive-research.md) describe ideas and tradeoffs;
they are dated planning documents, not a promise of supported features.

## License

[MIT](LICENSE) © 2026 Stvena contributors.

Stvena is an independent project, unaffiliated with OpenAI or Anthropic.

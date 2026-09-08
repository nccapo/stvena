# Stvena

**Your coding agent. Your changes. One terminal.**

<!-- demo:start -->
https://github.com/user-attachments/assets/7aecafe5-1e45-442e-ab49-82cfc3750d9f
<!-- demo:end -->

[![CI](https://github.com/nccapo/stvena/actions/workflows/ci.yml/badge.svg)](https://github.com/nccapo/stvena/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/nccapo/stvena)](https://github.com/nccapo/stvena/releases/latest)

Stvena runs your coding agent beside a live code review workspace. Keep the
agent's native terminal experience while you inspect changes, read complete
files, collect context, and run checks against the code you are reviewing.

Built in Go. Designed for terminal workflows with Codex and Claude Code.
**Early-stage software:** interfaces and local storage formats may change.

## Why Stvena?

- **See the work as it happens.** Review changes since the agent started, or
  inspect staged, unstaged, and new files across the workspace.
- **Read beyond the diff.** Browse the project tree, open complete files, and
  switch between unified and side-by-side views with syntax highlighting.
- **Give the agent precise context.** Select code with the mouse or keyboard,
  collect snippets across files, and paste an assembled draft into the agent.
  You decide when to submit it.
- **Keep track of your review.** Pin a captured version, mark files and hunks
  reviewed, and see when a reviewed change has changed again.
- **Check the code you are looking at.** Run a command in a temporary checkout
  of a captured version, inspect logs, and jump from recognized failures to source.
- **Stage deliberately.** Stage or unstage files and hunks with confirmation.

## Installation

You need **Git**, an interactive terminal, and your chosen agent CLI installed
and authenticated. Stvena uses Unix PTYs and process groups; macOS and Linux are
the intended platforms. Native Windows is not supported.

### Prebuilt binary (recommended)

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

Homebrew is not required. A dedicated Homebrew tap is not published yet, so the
verified installer is currently the shortest supported installation path.

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

Reopening a review compares a saved baseline with today's workspace; it does
not resume an agent conversation. Other commands can run in the agent pane,
but direct selection paste is supported for Codex and Claude Code.

## A typical workflow

1. Start `stvena` in your project and give the agent a task.
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
| **Ctrl-G** | Switch between agent and review |
| **Ctrl-Q** | Quit Stvena and stop the agent |
| **1 / 2 / 3** | Session changes / workspace changes / project files |
| **Enter / f** | Open file / return to file browser |
| **v** | Toggle diff and full-file view |
| **Drag / V** | Select code with mouse / keyboard |
| **b / x / B** | Paste selection / collect selection / open context |
| **P** | Pin the displayed version or resume live updates |
| **Space / N** | Mark file reviewed / next unreviewed file |
| **t / T** | Run a check / inspect results |
| **a / ?** | Actions menu / keyboard help |

Review shortcuts apply when review has focus. **Ctrl-C** keeps the agent's
native interrupt behavior. Review remains open after the agent exits; **q**
closes it. See the [complete user guide](docs/usage.md) for comments, hunk staging,
search, clipboard tools, and every shortcut.

## Privacy and local data

Stvena does not require its own API key or account. It launches the agent CLI
you already use with your environment and credentials. Your chosen agent's
network access and data handling still apply.

- Review metadata, comments, snippets, draft requests, preferences, and check
  output are stored under your OS cache directory in `stvena/<repository hash>`.
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
| `internal/ui` | Terminal layout, code rendering, and controls |

The [product direction](docs/product-direction.md) and
[research notes](docs/competitive-research.md) describe ideas and tradeoffs;
they are dated planning documents, not a promise of supported features.

## License

[MIT](LICENSE) © 2026 Stvena contributors.

Stvena is an independent project, unaffiliated with OpenAI or Anthropic.

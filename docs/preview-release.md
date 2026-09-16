# Stvena v0.4.0-preview.1 — review a project that is not a Git repository

This preview pairs Stvena's terminal workspace with the Stvena Live extension,
and removes the requirement that your project be a Git repository. A folder that
was never `git init`ed is captured into Stvena's own directory instead, so the
agent runs in the terminal, the editor opens and marks the files it touches, and
you accept or reject its changes — with nothing written into your source tree.

## Install

Requires macOS or Linux, the `git` command, VS Code or Antigravity, and an
installed, authenticated agent CLI such as Codex or Claude Code. Your project
does not have to be a repository.

1. Install the preview binary:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/nccapo/stvena/v0.4.0-preview.1/install.sh | \
     STVENA_VERSION=v0.4.0-preview.1 sh
   ```

   Alternatively, download and extract the archive matching your system:

   | System | Asset |
   | --- | --- |
   | macOS, Apple Silicon | `stvena_darwin_arm64.tar.gz` |
   | macOS, Intel | `stvena_darwin_amd64.tar.gz` |
   | Linux, ARM64 | `stvena_linux_arm64.tar.gz` |
   | Linux, Intel/AMD 64-bit | `stvena_linux_amd64.tar.gz` |

   Put the extracted `stvena` executable in a directory on your `PATH`.
   The installer verifies the archive against `checksums.txt`; that file also
   includes the VSIX checksum for manual verification.

2. Download [stvena-live-0.4.0.vsix](https://github.com/nccapo/stvena/releases/download/v0.4.0-preview.1/stvena-live-0.4.0.vsix).
   In VS Code or Antigravity's Command Palette, run
   **Extensions: Install from VSIX…** and choose the downloaded file.

3. Open a trusted local project, with or without Git. In its integrated terminal,
   run `stvena --version` and confirm `0.4.0-preview.1`, then run `stvena`.
   Ask the agent to edit and save a file. Stvena Live should navigate to the
   change while you continue typing in the terminal.

No Go or Node.js installation is needed. The ordinary installer continues to
select the stable version; previews require the explicit version above.
Extension updates are manual during this preview.

## What is new since v0.3.0-preview.1

- A project that is not a Git repository is reviewed the same way as one that
  is. Stvena captures it into a private store in its own cache directory and
  writes the editor descriptors beside that store — never into your project.
- The extension resolves a project with Git first and falls back to Stvena's
  bridge registry (`~/.stvena/bridges/`, or `$STVENA_HOME`), so a repository
  behaves exactly as it did before.
- **Branch (4)** and staging need a repository and say so. **Workspace (2)**
  becomes one list of everything that changed since the version Stvena first
  captured in that folder, with no staged/unstaged split.
- Because such a folder usually has no `.gitignore`, Stvena excludes the common
  dependency and build directories so the first capture does not hash your whole
  toolchain. The exclude file is named in the terminal and is yours to edit.
- Running `git init` later is picked up on the next launch, and the review pane
  says so while the current session keeps going.

## Preview scope

- Local macOS and Linux are supported targets; native Windows is unsupported.
  Remote extension hosts and other editor forks remain unverified.
- The `git` command is still required; only the repository is optional.
- Saved edits are sampled snapshots from every workspace writer, including you
  and formatters. Intermediate writes can coalesce; edits have no agent attribution.
- Read markers require supported agent hooks and any required hook trust.
  Live model-driven hook execution remains unverified; saved-edit following
  works independently. Read locations describe tool inputs, not model attention.
- Automatic following skips unsaved buffers and deleted files. Use one Stvena
  process per project.
- The bridge is local, with no listening network service or telemetry. The
  bridge registry is an owner-only map of paths; nothing in it is executed.

If the extension stays **Waiting**, check the binary version, workspace trust,
and that Stvena runs in that workspace. See **Stvena Live** in the Output panel
for errors and the [extension guide](https://github.com/nccapo/stvena/blob/v0.4.0-preview.1/extensions/vscode/README.md)
for hook setup and detailed limits.

Report problems through [GitHub Issues](https://github.com/nccapo/stvena/issues)
with your OS, editor version, Stvena version, agent CLI version, and reproduction
steps. Remove private project content from any logs or screenshots.

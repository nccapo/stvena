# Stvena v0.2.0-preview.1 — VS Code and Antigravity preview

This preview pairs Stvena's terminal workspace with the Stvena Live extension.
Saved file changes open at their source location while keyboard focus stays in
the integrated terminal. The extension includes pause/resume, an activity list,
and temporary read/edit line markers.

## Install

Requires macOS or Linux, Git, VS Code or Antigravity, and an installed,
authenticated agent CLI such as Codex or Claude Code.

1. Install the preview binary:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/nccapo/stvena/v0.2.0-preview.1/install.sh | \
     STVENA_VERSION=v0.2.0-preview.1 sh
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

2. Download [stvena-live-0.2.0.vsix](https://github.com/nccapo/stvena/releases/download/v0.2.0-preview.1/stvena-live-0.2.0.vsix).
   In VS Code or Antigravity's Command Palette, run
   **Extensions: Install from VSIX…** and choose the downloaded file.

3. Open a trusted local Git project. In its integrated terminal, run
   `stvena --version` and confirm `0.2.0-preview.1`, then run `stvena`.
   Ask the agent to edit and save a file. Stvena Live should navigate to the
   change while you continue typing in the terminal.

No Go or Node.js installation is needed. The ordinary installer continues to
select the stable version; previews require the explicit version above.
Extension updates are manual during this preview.

## Preview scope

- Local macOS and Linux are supported targets; native Windows is unsupported.
  Remote extension hosts and other editor forks remain unverified.
- Saved edits are sampled snapshots from every workspace writer, including you
  and formatters. Intermediate writes can coalesce; edits have no agent attribution.
- Read markers require supported agent hooks and any required hook trust.
  Live model-driven hook execution remains unverified; saved-edit following
  works independently. Read locations describe tool inputs, not model attention.
- Automatic following skips unsaved buffers and deleted files. Use one Stvena
  process per repository.
- The bridge is local, with no listening network service or telemetry.

If the extension stays **Waiting**, check the binary version, workspace trust,
and that Stvena runs in that workspace. See **Stvena Live** in the Output panel
for errors and the [extension guide](https://github.com/nccapo/stvena/blob/v0.2.0-preview.1/extensions/vscode/README.md)
for hook setup and detailed limits.

Report problems through [GitHub Issues](https://github.com/nccapo/stvena/issues)
with your OS, editor version, Stvena version, agent CLI version, and reproduction
steps. Remove private project content from any logs or screenshots.

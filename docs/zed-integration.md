# Zed integration research

Status: implemented, 2026-09-15. The recommendation below was built:
`stvena editor-lsp` ships in `internal/editorlsp` and the extension wrapper is
[nccapo/zed-stvena](https://github.com/nccapo/zed-stvena). The subcommand is
`editor-lsp --ide zed` rather than `zed-lsp`, so other LSP editors can launch
the same server. Claims marked **verified** were checked against a fresh clone
of `zed-industries/zed` (main) and the `zed_extension_api` 0.7.0 / WIT
`since_v0.8.0` surface; claims marked **verify** still need an editor-host test
before we rely on them.

## Summary

Zed cannot run the VS Code extension and its own extension API cannot express
most of what Stvena Live does. Zed extensions are WebAssembly components that
the host calls at a few fixed points (start a language server, answer a slash
command, start an MCP server, resolve a debug adapter). There is **no API** for
editor commands, decorations, panels, tree views, status-bar items, file
watching, or opening a file — and the WASI filesystem is limited to the
extension's own work directory (**verified**, `crates/extension_host/src/wasm_host.rs`
preopens only `work_dir/<extension-id>`). The "agent server" extension type was
deprecated in Zed v1.5.0 in favor of the ACP Registry, and MCP-server extensions
are scheduled for the same fate.

The one escape hatch Zed leaves open is the Language Server Protocol. Zed's LSP
client is rich enough that a **language server can drive most of the Stvena
Live surface**: it can open a file at a range without taking focus
(`window/showDocument`, **verified** to handle local `file://` URIs), put
clickable ✓ Accept / ✗ Reject buttons above hunks (code lens, **verified**,
executes via `workspace/executeCommand`), add selection-scoped context-menu
actions (code actions), label lines (inlay hints), list unreviewed hunks in the
Diagnostics panel (`publishDiagnostics`), and show a persistent status-bar item
(`$/progress`). Other extensions already use this trick
([zed-discord-presence](https://github.com/xhyrom/zed-discord-presence)).

**Recommendation:** ship a Zed extension whose only job is to locate or download
the `stvena` binary and launch it as a language server (`stvena zed-lsp` or a
generic `stvena editor-lsp --ide zed`). All bridge logic (descriptor polling,
`git cat-file`, request writing, presence heartbeat) moves into Go, next to the
code that already produces the descriptors. The VS Code extension keeps its JS
implementation; the Go LSP becomes the second consumer of the same protocol,
which is what `docs/editor-integration.md` was written for.

## Why not the other routes

**Reuse the VSIX.** Impossible; Zed has no VS Code compatibility layer and no
JavaScript extension host.

**ACP (Agent Client Protocol).** Zed's agent panel runs Claude Code and Codex
natively over ACP, with per-hunk keep/reject in a review multibuffer, inline
single-file review (`agent.single_file_review`), checkpoints, and a follow mode
that tracks the agent's reads and edits (**verified** in `crates/agent_ui`,
`crates/action_log`). That is essentially Stvena Live's feature list, built into
the editor. Custom agents are configured in `settings.json` under
`agent_servers` with `type = "custom"`. Making Stvena an ACP agent would mean
replacing the terminal/PTY model with Zed's chat panel, which is not the product.
The honest positioning: in Zed, Stvena is for people who run the agent in a
terminal (Zed's integrated terminal or a separate one) and want review without
adopting the agent panel. Worth stating in the README so the comparison is
ours rather than a reviewer's.

**CLI only, no extension.** `zed -a path/to/file:42` opens a file at a line in
the focused workspace (**verified**, `crates/cli/src/main.rs`; `-e/--existing`
also available). Stvena could call this for follow navigation with zero
extension work, but it steals focus, gives no markers, no accept/reject, and no
presence heartbeat, so `stvena-ide.json` could never be written and IDE mode
would never turn on. Acceptable as a day-one "Open in Zed" keybinding inside
the TUI, not as the integration.

**Settings-only language server.** Not possible. User settings can reorder or
disable language servers, but a name that no extension registered resolves to
nothing (**verified**, `crates/project/src/manifest_tree/server_tree.rs`). The
extension is required to get our binary launched.

## Capability map

| Stvena Live (VS Code) | Zed route | Fidelity | Notes |
| --- | --- | --- | --- |
| Poll `stvena-live.json` / `stvena-review.json` | LS process polls the Git dir itself | Full | Go code, not WASM; no sandbox limits |
| Write `stvena-request.json`, `stvena-ide.json` | LS writes them | Full | `ide = "zed"`, version from extension manifest |
| Open working file at changed line, keep terminal focus | `window/showDocument { takeFocus: false, selection }` | **Not in Zed 1.19** | Zed 1.19.2 answers `Unrecognized method`, and `window/showDocument` is absent from its LSP handler table; the handler seen on `main` has not shipped. The server gates the request on the client advertising `window.showDocument.support`, says so once in the LSP log, and keeps marking locations instead of opening them |
| Skip unsaved buffers | LS tracks `didChange` vs `didSave` per document | Full | Zed sends full/incremental sync; treat any unsaved change as dirty |
| ✓ Accept / ✗ Reject above each hunk | `textDocument/codeLens` + `workspace/executeCommand` | Good | User must set `"code_lens": "on"` (off by default, **verified** in PR #54100). Refresh via `workspace/codeLens/refresh` **verified** handled; issue #58864 reports it is unreliable after dynamic registration, so register statically and re-publish on `didSave`/`didOpen` as a fallback |
| Reject with reason… | No LSP text prompt | Gap | `showMessageRequest` offers buttons only. Options: a code action that sends `reject` and a `showMessage` telling the user to add the reason in the TUI; or accept a canned set of reasons as separate lens items |
| Context menu: Review / Add to context / Ask agent / Accept file / Reject file | Code actions on the selection | Good | Zed shows them in the code-actions menu, not the right-click menu; keybinding is Zed's `editor::ToggleCodeActions`, not a custom command |
| Line tint for read / edit / review / accepted / rejected | Nothing equivalent | Partial | Zed has no background-decoration API. Closest: inlay hint label at line start (e.g. `✎ agent edit`, `👁 read (claude)`, `◆ reviewing`) plus an Information-severity diagnostic on the range, which draws an underline and a scrollbar marker (**verified**, `display_map.rs`, `element.rs`) |
| Gutter icons, overview-ruler markers | Diagnostic severity markers | Partial | Only Error/Warning/Information reach the scrollbar |
| Explorer badges + "Stvena Live" tree view | Diagnostics panel | Substitute | One Information diagnostic per unreviewed hunk (`Stvena: unreviewed change`) makes Zed's Project Diagnostics panel the review queue, with click-to-jump for free. Reviewed and rejected hunks get Hint severity or are dropped |
| Status bar "Following" / pending rejections | `$/progress` work-done item | Substitute | Shown in the activity indicator (**verified**, `crates/activity_indicator`). Keep one long-lived progress token, update its message. Not clickable; expose "Apply rejections now" as a workspace-level code action instead |
| Pause / Resume following | Code action or `showMessageRequest` toggle | Partial | No toggle button; a code action "Stvena: Pause following" on any line works |
| Output channel for errors | `window/logMessage` → Zed LSP logs | Full | `dev: open language server logs` |
| Feature gating on `features` | Same, in Go | Full | |
| `git cat-file` blob reads | Same, in Go | Full | Reuse existing capture code |

## Hard limits to design around

Language attachment is by name, with no wildcard. `[language_servers.<id>]
languages = [...]` must enumerate Zed language names exactly as their
`config.toml` declares them (**verified**, `extension_manifest.rs`;
`LanguageServerManifestEntry.languages: Vec<LanguageName>`). A file whose
language is not in that list gets no Stvena server and therefore no markers or
actions. We should list every built-in language plus the popular
extension-provided ones (Go, Rust, JavaScript, TypeScript, TSX, Python, JSON,
JSONC, YAML, TOML, Markdown, HTML, CSS, SCSS, Shell Script, C, C++, Swift,
Kotlin, Java, Ruby, PHP, SQL, Dockerfile, Plain Text, ...). **Verify** that
listing a language whose extension is not installed is ignored rather than
rejected at load time.

One server per worktree, started lazily. Zed launches a language server the
first time a buffer of a matching language opens in that worktree, and it only
knows about open buffers. Automatic follow of a file the user has never opened
still works because `window/showDocument` opens it. The server must resolve the
Git root itself from the worktree root path it is given (`git rev-parse
--show-toplevel` / `--absolute-git-dir`, exactly as the VS Code extension does)
and must tolerate the worktree being a subfolder of the repository.

Code lens is opt-in and vertically shifts lines when it appears. Document the
setting in the README and consider making inlay-hint labels carry the full
surface when lenses are off, so the extension degrades to "markers only" rather
than to nothing.

No arbitrary keybindings. Users bind Zed actions, not ours; we can document a
keymap snippet that binds `editor::ToggleCodeActions` in a Stvena-friendly way
but cannot ship one.

Dirty-buffer detection differs. VS Code gave us `document.isDirty`; in LSP we
infer it from text-sync notifications, which is reliable as long as Zed's
`textDocument/didSave` includes or follows every save (it does; **verify**
behavior for external writes by the agent to an open buffer, which Zed reloads
from disk and reports as `didChange` without a `didSave`).

## Proposed architecture

```
Zed  ──stdio LSP──▶  stvena zed-lsp  ──poll/write──▶  .git/stvena-*.json  ◀── stvena (TUI, PTY agents)
 ▲                        │
 └── extension (WASM):    └── git cat-file, request writer, presence heartbeat
     locate/download binary, launch it
```

Extension repository layout (published extensions must be their own Git repo,
so this lives at `nccapo/zed-stvena` or `extensions/zed` mirrored to a repo):

```
extension.toml
Cargo.toml
src/lib.rs
LICENSE
README.md
```

`extension.toml`:

```toml
id = "stvena"
name = "Stvena Live"
version = "0.1.0"
schema_version = 1
authors = ["Nicolas <ggdnicolas@gmail.com>"]
description = "Accept or reject agent changes in your editor and follow agent reads and edits."
repository = "https://github.com/nccapo/zed-stvena"

[language_servers.stvena]
name = "Stvena Live"
languages = ["Go", "Rust", "JavaScript", "TypeScript", "TSX", "Python", "JSON", "JSONC",
             "YAML", "TOML", "Markdown", "HTML", "CSS", "SCSS", "Shell Script", "C", "C++",
             "Swift", "Kotlin", "Java", "Ruby", "PHP", "SQL", "Dockerfile", "Plain Text"]
code_action_kinds = ["source.stvena"]

[capabilities]
capabilities = [
  { kind = "download_file", host = "github.com", path = ["nccapo", "stvena", "**"] },
]
```

`src/lib.rs` (the entire extension; everything else is Go):

```rust
use zed_extension_api::{self as zed, LanguageServerId, Result};

struct Stvena;

impl zed::Extension for Stvena {
    fn new() -> Self { Stvena }

    fn language_server_command(
        &mut self,
        _id: &LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command> {
        // Prefer the user's installed binary so the TUI and the LS are the same build.
        let path = worktree
            .which("stvena")
            .ok_or("stvena not found on PATH; install it with brew install nccapo/stvena/stvena")?;
        Ok(zed::Command {
            command: path,
            args: vec!["zed-lsp".into()],
            env: worktree.shell_env(),
        })
    }
}

zed::register_extension!(Stvena);
```

A later version can fall back to `latest_github_release` + `download_file` when
`which` fails, mirroring how language-server extensions install their servers.
Keeping v0.1 to `which` avoids a version skew between the TUI and the LS, which
matters because both read the same descriptor schema.

Go side, new package `internal/editorlsp` (name to taste) and a `zed-lsp`
subcommand:

- `initialize`: advertise `codeLensProvider { resolveProvider: false }`,
  `codeActionProvider { codeActionKinds: ["source.stvena"] }`,
  `executeCommandProvider { commands: [...] }`, `inlayHintProvider`,
  `textDocumentSync = Incremental` with `save = true`, and `workspace/didChangeWatchedFiles`
  registration for the Git dir if we want push instead of the 700 ms poll
  (**verified** Zed supports the registration; polling is fine and matches VS Code).
- Descriptor loop: same validation rules as `extensions/vscode/src/bridge.js`.
  On change: re-publish diagnostics, send `workspace/codeLens/refresh` and
  `workspace/inlayHint/refresh`, update the `$/progress` message, and if
  following is on and the target buffer is not dirty, send `window/showDocument`.
- Commands (`stvena.accept`, `stvena.reject`, `stvena.undoReject`,
  `stvena.acceptFile`, `stvena.rejectFile`, `stvena.applyRejections`,
  `stvena.review`, `stvena.context`, `stvena.prompt`, `stvena.toggleFollow`,
  `stvena.nextUnreviewed`) write `stvena-request.json` and optimistically update
  the lens text, exactly as the VS Code extension does; roll back on a refused
  `lastRequest`.
- `stvena-ide.json` heartbeat every ≤30 s with `ide = "zed"`.
- Everything the LS accepts is gated on `features` from the descriptor.

Because the LS is a subcommand of the same binary, the Go tests can cover the
whole bridge without an editor, and the descriptor code is shared rather than
ported. The VS Code extension stays JS; the two consumers are cross-checked by
the existing protocol tests.

## Development and distribution

Local development needs Rust (`rustup`) with the `wasm32-wasip2` target; Zed
builds the WASM itself when you run `zed: install dev extension` and point it
at the extension directory. Run `zed --foreground` to see extension and
language-server logs, or use `dev: open language server logs` in the editor.
Rebuilding the Go binary does not require reinstalling the extension.

Publishing is a pull request to
[zed-industries/extensions](https://github.com/zed-industries/extensions): add
the repo as a Git submodule under `extensions/stvena` and an entry in
`extensions.toml`. Updates are further PRs bumping the submodule and version.
There is no marketplace listing page to control; the README in the extension
repo is what users see.

## Host verification, 2026-09-15

Run against Zed 1.19.2 (Apple Silicon) with the extension installed from a local
build and `stvena editor-lsp --ide zed` on `PATH`, driving a disposable Git
project with fixture descriptors:

- The extension loads and Zed launches `stvena editor-lsp --ide zed` for a Go
  buffer. `stvena-ide.json` appears, so the TUI would offer IDE mode.
- Zed requests `textDocument/codeLens`, `textDocument/inlayHint` and
  `textDocument/codeAction`, and re-requests both after
  `workspace/codeLens/refresh` and `workspace/inlayHint/refresh` — those
  refreshes work in 1.19.2.
- Returned and rendered: `✓ Accept` / `✗ Reject` lenses, `👁 read (claude)` and
  `◆ reviewing` hints, `Stvena: unreviewed agent change` diagnostics, and the
  status item `following · 1 unreviewed · 1 rejection waiting for turn end`.
- `window/showDocument` is rejected (see the capability map), so follow
  navigation is marker-only on this version.
- Two settings are required, both off by default: `"code_lens": "on"` for the
  accept/reject buttons and `"inlay_hints": { "enabled": true }` for the marker
  labels. Without them Zed never asks for those surfaces.
- Zed reports the **opened file** as the worktree root when a single file is
  opened (`workspaceFolders[0].uri` is the file, while `rootUri` is the folder),
  so the server resolves a file root to its parent directory.
- Zed asks for language servers only in a trusted worktree; an untrusted folder
  logs `Waiting for worktree … to be trusted` and starts nothing.

Still unverified by a human: clicking a lens or a code action, which is what
sends `workspace/executeCommand`. The command handlers are covered by
`go test ./internal/editorlsp/`.

## Smoke test to add

Mirror the VS Code host test in a disposable project: a Go test that speaks LSP
over stdio to `stvena zed-lsp` and asserts `showDocument` requests, lens
contents, diagnostics, and request-file writes against fixture descriptors.
Then a manual Zed checklist: Go and Rust file get lenses with `code_lens: on`;
an unlisted language (e.g. a niche extension language) gets nothing and the
README says so; unsaved buffer suppresses follow and lenses; refused request
rolls back the lens; `stvena-ide.json` appears within 30 s of opening the
project and IDE mode switches on in the TUI.

## Open questions

Whether to publish a single `stvena editor-lsp --ide zed` that other LSP-capable
editors (Helix, Neovim via a one-line config) could reuse for free — Helix and
Neovim let users register any language-server binary in settings, so one Go
server would give three editors for the price of one. Zed is the only one of
the three that needs an extension wrapper.

Whether the Diagnostics-panel-as-review-queue substitute is acceptable UX or
whether unreviewed hunks should stay lens-only to avoid polluting a panel users
associate with compiler errors.

Whether Zed's terminal reports itself in a way Stvena can detect for the
"editor might be present" hint; the presence heartbeat already makes this
unnecessary for correctness.

## Sources

- [Developing Extensions](https://zed.dev/docs/extensions/developing-extensions), [Extension Capabilities](https://zed.dev/docs/extensions/capabilities), [Language Extensions](https://zed.dev/docs/extensions/languages)
- [Agent Server Extensions (deprecated)](https://zed.dev/docs/extensions/agent-servers), [MCP Server Extensions](https://zed.dev/docs/extensions/mcp-extensions), [The ACP Registry is Live](https://zed.dev/blog/acp-registry), [Agent Panel](https://zed.dev/docs/ai/agent-panel)
- [Life of a Zed Extension: Rust, WIT, Wasm](https://zed.dev/blog/zed-decoded-extensions), [zed_extension_api 0.7.0](https://docs.rs/zed_extension_api/latest/zed_extension_api/)
- [Support code lens in the editor #54100](https://github.com/zed-industries/zed/pull/54100), [codeLens/refresh issue #58864](https://github.com/zed-industries/zed/issues/58864), [showDocument discussion #53123 (superseded by current source)](https://github.com/zed-industries/zed/discussions/53123)
- [CLI Reference](https://zed.dev/docs/reference/cli), [zed-discord-presence (LSP workaround precedent)](https://github.com/xhyrom/zed-discord-presence)
- Source verification: `zed-industries/zed` main as of 2026-09-15 — `crates/extension_api/wit/since_v0.8.0/`, `crates/extension_host/src/wasm_host.rs`, `crates/extension/src/extension_manifest.rs`, `crates/project/src/lsp_store.rs`, `crates/project/src/lsp_store/dynamic_registration.rs`, `crates/editor/src/items.rs`, `crates/project/src/manifest_tree/server_tree.rs`, `crates/cli/src/main.rs`, `docs/src/ai/external-agents.md`

# Stvena product direction

Explored 8 September 2026 and updated 11 September 2026. Later sections include
implementation status as well as proposals. Research
uses official documentation, not hands-on competitor benchmarks. The local
README and implementation were checked to distinguish additions from existing
functionality.

Stvena should help people understand, direct and verify work in their existing
agent CLI. The proposed primary workflow is: find relevant code, build a precise
request, inspect the resulting changes, and resolve failures without repeatedly
reconstructing context.

## 1. Project explorer and quick open

Browse unchanged files as well as changed ones. Offer a familiar tree, filename
search, a Changed files filter, and recent files. Open a file, select code, and
use the existing agent handoff before any agent edits exist.

Current gap: the file list is derived from changes; full-file mode expands only
the selected changed file. It is not a general project explorer.

First scope: nonignored repository files, source search, stable read-only file
views, and explicit provenance when selections are captured. Keep unchanged
files separate from review counts and staging actions. Symbol navigation can
follow with supported language parsers or language servers.

Success: in a clean repository, a user locates a function and adds its source
and absolute path to the CLI draft without first changing a file.

## 2. Context tray

Collect several selections, file references, and check failures before handing
them to the agent. A compact bottom tray shows each attachment, its path and line
range, and removal controls. Let the user preview the assembled request, add a
question and place it into the native CLI draft.

Example: attach the login handler, its test, and a failing assertion; ask the
agent to fix the failure while preserving the public API.

Current gap: b transfers one selection immediately. Saved comments and feedback
export exist, but are a separate review workflow rather than a general attachment
composer.

First scope: multiple captured code slices, deduplication, clear size limits,
per-item version provenance, stale indicators, and a recoverable local draft.
Preserve b as the immediate handoff shortcut. Do not change Enter behavior in
the native CLI or assume delivery means the agent has read the request.

Success: three attachments from two files arrive in one draft, with exact paths
and source ranges, and can be individually removed before handoff.

## 3. Problems and checks

Turn supported test/compiler/linter failures into a navigable Problems list.
Click a failure to open its source; choose Add to agent to attach the command,
relevant output, source context and tested version. Offer a rerun action and
retain the existing stale-result indication.

Current foundation: explicit commands already run on captured code and record
exit codes, output and version freshness. The missing pieces are structured
navigation and the failure-to-agent workflow.

First scope: parsers for a small documented set of output formats, with raw logs
always available for unsupported output. Resolve temporary-checkout paths back
to repository paths and distinguish tested source from today's source.

Success: a failed check can be opened at the reported source line and attached
to a draft without copying terminal output by hand.

## 4. Session timeline and bookmarks

A chronological list of observed edit batches, review events and check runs.
Users can name checkpoints such as "before refactor" and compare two checkpoints.
A return-to-session view summarizes unfinished reviews, stale checks and saved
notes using recorded facts.

Current gap: baseline/latest snapshots and saved review versions exist, but do
not form a browsable, retained history of every edit batch or conversation turn.

First scope: retained manual checkpoints and observed change batches, with a
storage limit. Use agent integrations for genuine turn boundaries; filesystem
observations alone cannot identify the author or intention of an edit. Start
with inspect/compare; a restore workflow needs recovery and concurrent-edit
handling. Session notes are not a replacement for resuming the native CLI chat.

Success: a user returning later can compare two named versions and locate changes
made after their last review.

## 5. Related code and impact navigation

For the selected function, show definitions, references, imports, and related
tests as clickable lists. Allow selected related code to enter the context tray.
A compact outline is more useful as a first step than a large dependency graph.

Current gap: source text search exists, but symbol/reference navigation does not.

First scope: one or two supported languages, precise parser/language-server
results, and clearly labeled filename/text heuristics elsewhere. References
indicate relationships, not proof that behavior or runtime callers are affected.

Success: a user can find a changed function's known callers and inspect its test
without leaving the pane.

## 6. Task brief with evidence

A small user-authored list of desired outcomes and constraints, with links to
code, comments and check runs. Example: "expired sessions are rejected" can link
to the relevant test result; "keep public API unchanged" can link to a review.

Current gap: review marks track code versions, not whether the requested behavior
has been demonstrated.

First scope: manual checklist and evidence links. Do not infer task completion
from green tests or from an agent saying it is finished. When referenced code
changes, show that attached evidence may be stale.

Success: users can identify which task outcomes have evidence and which still
need checking, without reading an entire terminal conversation.

## Presentation and implementation order

Use familiar bottom navigation: Files, Changes, Checks, Session. Only one view
occupies the pane. Context is a compact attachment tray that appears when needed;
related code lives inside the file view. Keep existing shortcuts and the native
CLI as the place where requests are submitted.

1. Project explorer and quick open — medium effort; enables pre-edit use.
2. Context tray — medium effort; develops the current selection handoff.
3. Problems navigation and failure handoff — medium effort per supported format.
4. Session checkpoints/timeline — medium to large; durable history and retention.
5. Symbol/reference navigation — large across languages; start narrowly.
6. Task brief with linked evidence — medium; validate demand with a manual version.

Effort is relative engineering judgment, not a delivery estimate. Prioritize
observed reductions in repeated context copying, navigation mistakes, and time
to understand a failure. More panels or longer time spent in the tool are not
success criteria on their own.

## Research references

- [VS Code inline chat](https://code.visualstudio.com/docs/chat/inline-chat)
  supports prompts scoped to selected code and routes requests into an existing
  editing session. This supports keeping code and conversation closely connected;
  a selection action alone is not an exclusive feature.
- [Aider's repository map](https://aider.chat/docs/repomap.html) exposes important
  symbols and relationships to its model. Stvena could make related source
  navigable for the human and attachable to the existing CLI request.
- [Conductor checkpoints](https://www.conductor.build/docs/reference/checkpoints)
  provide turn-based snapshots and restore. Stvena's proposed timeline would
  connect its own observed versions with human review and local checks; checkpoint
  support itself is established functionality elsewhere.

The opportunity is the quality of this combined terminal workflow. This research
does not establish that any proposal is absent from all competing products.

## Implementation update — 11 September 2026

The first four builds now have working initial implementations:

- **Project files:** a searchable list of all captured files, including unchanged
  files, with full-file viewing and existing mouse/keyboard selection. This first
  version now groups files into expandable folders with mouse and keyboard
  navigation. Recent-file history and repository-wide source search remain future
  refinements.
- **Context tray:** collect multiple versioned code slices and check failures,
  remove individual attachments, save a request, preview a wrapped draft, and
  copy or paste it into the native agent CLI without submitting. Drafts persist
  locally, with 20-item/32-KiB limits and earlier-snapshot indications.
- **Problems:** parse common colon locations, tsc locations and Python traceback
  locations; open tested source; collect failures with nearby captured code; and
  explicitly rerun checks. Raw logs remain available. Ambiguous/generated paths
  can be collected with a source-unavailable explanation instead of guessed code.
- **Session timeline:** retain up to 100 stable tree transitions observed by the
  700 ms capture loop, browse them chronologically, pin a batch, and count newer
  observed batches. These are explicitly observations rather than agent-turn or
  authorship claims. Named bookmarks, arbitrary checkpoint comparison, review
  event history, and restore remain future work.

Review inbox filtering, a merge-base Branch changes source, semantic live cursor
anchoring, and a bidirectional source-only VS Code review bridge were also added.
The editor continues to open normal working files; Stvena remains the sole diff
surface.

Symbol/reference navigation and the task evidence checklist remain proposals.
The README is the current guide to supported controls and limits.

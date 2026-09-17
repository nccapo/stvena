package editorlsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/editor"
)

// codeActionKind is the kind the Zed extension declares, so the editor can
// group and bind these actions.
const codeActionKind = "source.stvena"

// progressToken names the single long-lived work-done item this server keeps in
// the editor's status area.
const progressToken = "stvena/status"

var commandNames = []string{
	"stvena.accept", "stvena.unaccept", "stvena.reject", "stvena.undoReject",
	"stvena.acceptFile", "stvena.rejectFile", "stvena.applyRejections",
	"stvena.nextUnreviewed", "stvena.review", "stvena.context", "stvena.prompt",
	"stvena.toggleFollow", "stvena.showLatest",
}

// target is the argument every located command carries. It is the same shape
// the code lens and the code action put in `arguments`.
type target struct {
	Path    string `json:"path"`
	HunkID  string `json:"hunkId,omitempty"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	Text    string `json:"text,omitempty"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type command struct {
	Title     string   `json:"title"`
	Command   string   `json:"command"`
	Arguments []target `json:"arguments,omitempty"`
}

func (s *server) showMessage(kind int, text string) {
	_ = s.conn.notify("window/showMessage", map[string]any{"type": kind, "message": text})
	_ = s.conn.notify("window/logMessage", map[string]any{"type": kind, "message": text})
}

// reviewFile returns the published review state for a repository-relative path.
func (s *server) reviewFile(path string) *editor.ReviewFile {
	if s.review == nil || path == "" {
		return nil
	}
	for i := range s.review.Files {
		if s.review.Files[i].Path == path {
			return &s.review.Files[i]
		}
	}
	return nil
}

func decisionKey(path, hunkID string) string { return path + "\x00" + hunkID }

// hunkState folds Stvena's published state together with any decision this
// editor has sent but not yet seen confirmed, so a click responds at once.
func (s *server) hunkState(file *editor.ReviewFile, hunk *editor.ReviewHunk) (reviewed, rejected, waiting bool) {
	id := ""
	if hunk != nil {
		reviewed, rejected = hunk.Reviewed, hunk.Rejected
		id = hunk.ID
	} else {
		reviewed, rejected = file.Reviewed, file.Rejected
	}
	pending := s.optimistic[decisionKey(file.Path, id)]
	if pending == nil {
		return reviewed, rejected, false
	}
	switch pending.kind {
	case "accept":
		return true, false, true
	case "unaccept":
		return false, rejected, true
	case "reject":
		return reviewed, true, true
	case "undo-reject":
		return reviewed, false, true
	}
	return reviewed, rejected, false
}

// lineCount reports how many lines a known buffer has, so ranges never point
// past the end of the file the editor is showing.
func (doc *document) lineCount() int {
	if !doc.known {
		return 0
	}
	return strings.Count(doc.text, "\n") + 1
}

func clampLine(line, count int) int {
	if line < 0 {
		line = 0
	}
	if count > 0 && line > count-1 {
		line = count - 1
	}
	return line
}

// renderable reports whether a buffer can carry Stvena's markers: it must be
// inside the repository and match what Stvena captured.
func (s *server) renderable(uri string) (*document, bool) {
	doc := s.docs[uri]
	if doc == nil || doc.path == "" || doc.dirty {
		return doc, false
	}
	return doc, liveReview(s.review, time.Now())
}

func (s *server) replyCodeLens(msg *message) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	lenses := []map[string]any{}
	if s.decode(msg, &params) {
		if doc, ok := s.renderable(params.TextDocument.URI); ok {
			lenses = s.lensesFor(doc)
		}
	}
	_ = s.conn.reply(msg.ID, lenses)
}

func (s *server) lensesFor(doc *document) []map[string]any {
	out := []map[string]any{}
	file := s.reviewFile(doc.path)
	if file == nil {
		return out
	}
	count := doc.lineCount()
	for i := range file.Hunks {
		hunk := &file.Hunks[i]
		line := clampLine(hunk.Start-1, count)
		at := lspRange{Start: position{Line: line}, End: position{Line: line}}
		ref := target{Path: file.Path, HunkID: hunk.ID, Line: hunk.Start, EndLine: hunk.End}
		reviewed, rejected, waiting := s.hunkState(file, hunk)
		suffix := ""
		if waiting {
			suffix = " …"
		}
		add := func(title, name string) {
			out = append(out, map[string]any{"range": at,
				"command": command{Title: title, Command: name, Arguments: []target{ref}}})
		}
		switch {
		case rejected:
			title := "✗ Rejected" + suffix + " · Undo"
			if file.Rejected {
				title = "✗ Rejected" + suffix + " · Undo file rejection"
			}
			if supports(s.review, "undo-reject") {
				add(title, "stvena.undoReject")
			}
		case reviewed:
			if supports(s.review, "unaccept") {
				add("✓ Accepted"+suffix+" · Undo", "stvena.unaccept")
			}
		default:
			if supports(s.review, "accept") {
				add("✓ Accept"+suffix, "stvena.accept")
			}
			if supports(s.review, "reject") {
				// An added file cannot have one block reverted on its own.
				if file.Status == "A" {
					add("✗ Reject file"+suffix, "stvena.rejectFile")
				} else {
					add("✗ Reject"+suffix, "stvena.reject")
				}
			}
		}
	}
	return out
}

func (s *server) replyCodeActions(msg *message) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Range lspRange `json:"range"`
	}
	actions := []map[string]any{}
	if s.decode(msg, &params) {
		actions = s.actionsFor(params.TextDocument.URI, params.Range)
	}
	_ = s.conn.reply(msg.ID, actions)
}

func (s *server) actionsFor(uri string, selection lspRange) []map[string]any {
	out := []map[string]any{}
	doc, ok := s.renderable(uri)
	if !ok {
		// Following can still be paused or resumed from any buffer, but the
		// located actions need a clean file Stvena owns.
		if doc != nil && doc.dirty {
			return out
		}
	}
	add := func(title, name string, ref target) {
		out = append(out, map[string]any{
			"title": title, "kind": codeActionKind,
			"command": command{Title: title, Command: name, Arguments: []target{ref}},
		})
	}
	if ok {
		line := selection.Start.Line + 1
		endLine := selection.End.Line + 1
		// A selection that ends at the start of a line does not include it.
		if endLine > line && selection.End.Character == 0 {
			endLine--
		}
		if endLine < line {
			endLine = line
		}
		ref := target{Path: doc.path, Line: line, EndLine: endLine}
		if supports(s.review, "review") {
			add("Stvena: Review This Line in Stvena", "stvena.review", ref)
		}
		if supports(s.review, "context") {
			add("Stvena: Add Selection to Context", "stvena.context", ref)
		}
		if supports(s.review, "prompt") {
			add("Stvena: Ask the Agent About This Selection", "stvena.prompt", ref)
		}
		if supports(s.review, "accept") {
			add("Stvena: Accept All Changes in This File", "stvena.acceptFile", target{Path: doc.path, Line: 1, EndLine: 1})
		}
		if supports(s.review, "reject") {
			add("Stvena: Reject All Changes in This File", "stvena.rejectFile", target{Path: doc.path, Line: 1, EndLine: 1})
		}
		if supports(s.review, "next-unreviewed") {
			add("Stvena: Next Unreviewed File", "stvena.nextUnreviewed", target{})
		}
		if s.review != nil && s.review.Pending != nil && s.review.Pending.Count > 0 &&
			supports(s.review, "apply-rejections") {
			add("Stvena: Apply Rejected Changes Now", "stvena.applyRejections", target{})
		}
	}
	if s.following {
		add("Stvena: Pause Following", "stvena.toggleFollow", target{})
	} else {
		add("Stvena: Resume Following", "stvena.toggleFollow", target{})
	}
	return out
}

func (s *server) replyInlayHints(msg *message) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	hints := []map[string]any{}
	if s.decode(msg, &params) {
		hints = s.hintsFor(params.TextDocument.URI, time.Now())
	}
	_ = s.conn.reply(msg.ID, hints)
}

func (s *server) hintsFor(uri string, now time.Time) []map[string]any {
	hints := []map[string]any{}
	doc := s.docs[uri]
	// An unsaved buffer no longer matches the captured lines, so a marker on it
	// would point at the wrong code.
	if doc == nil || doc.path == "" || doc.dirty {
		return hints
	}
	count := doc.lineCount()
	add := func(m *marker) {
		if m == nil || m.path != doc.path {
			return
		}
		hints = append(hints, map[string]any{
			"position":     position{Line: clampLine(m.line-1, count), Character: 0},
			"label":        markerLabel(m),
			"paddingRight": true,
		})
	}
	if s.following {
		if s.current != nil && fresh(s.current.at, now) {
			add(s.current)
		}
		// The review focus is persistent: it marks where the user is reviewing,
		// not a transient event.
		if liveReview(s.review, now) {
			add(s.reviewMarker())
		}
	}
	return hints
}

func markerLabel(m *marker) string {
	switch m.kind {
	case "read":
		if !m.rangeKnown {
			return "👁 read (range unavailable)"
		}
		if m.agent == "" {
			return "👁 read"
		}
		return "👁 read (" + m.agent + ")"
	case "review":
		return "◆ reviewing"
	default:
		return "✎ agent edit"
	}
}

// publishDiagnostics makes the editor's diagnostics panel the review queue: one
// Information item per unreviewed block, one Hint per queued rejection.
func (s *server) publishDiagnostics(now time.Time) {
	if s.bridge == nil {
		return
	}
	next := map[string][]map[string]any{}
	if liveReview(s.review, now) {
		for i := range s.review.Files {
			file := &s.review.Files[i]
			uri := pathToURI(filepath.Join(s.bridge.root, filepath.FromSlash(file.Path)))
			items := []map[string]any{}
			for j := range file.Hunks {
				hunk := &file.Hunks[j]
				reviewed, rejected, _ := s.hunkState(file, hunk)
				severity, message := 3, "Stvena: unreviewed agent change"
				switch {
				case rejected:
					severity = 4
					message = "Stvena: rejected, reverts " + appliesAtText(s.review.Pending)
				case reviewed:
					continue
				}
				items = append(items, map[string]any{
					"range": lspRange{
						Start: position{Line: hunk.Start - 1},
						End:   position{Line: hunk.End},
					},
					"severity": severity, "source": "stvena", "message": message,
				})
			}
			if len(items) > 0 {
				next[uri] = items
			}
		}
	}
	for uri, items := range next {
		// Diagnostics are republished on every descriptor change, which is what
		// keeps the panel correct even where codeLens/refresh is unreliable;
		// sending an identical list again would only cost round trips.
		encoded := compact(items)
		if s.published[uri] == encoded {
			continue
		}
		_ = s.conn.notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": items})
		s.published[uri] = encoded
	}
	for uri := range s.published {
		if _, still := next[uri]; still {
			continue
		}
		_ = s.conn.notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": []any{}})
		delete(s.published, uri)
	}
}

func appliesAtText(pending *editor.PendingRejections) string {
	if pending == nil {
		return "when the agent finishes its turn"
	}
	switch pending.AppliesAt {
	case "now":
		return "now"
	case "manual":
		return "when you apply the queue"
	default:
		return "when the agent finishes its turn"
	}
}

// refreshLenses and refreshHints ask the editor to re-request what it drew.
// Zed's codeLens refresh handling has a reported gap, so diagnostics are
// republished on every descriptor change as well; the panel stays correct even
// if lenses lag.
func (s *server) refreshLenses() { _ = s.conn.request("workspace/codeLens/refresh", nil) }
func (s *server) refreshHints()  { _ = s.conn.request("workspace/inlayHint/refresh", nil) }

func (s *server) startProgress() {
	if s.progress {
		return
	}
	_ = s.conn.request("window/workDoneProgress/create", map[string]any{"token": progressToken})
	_ = s.conn.notify("$/progress", map[string]any{
		"token": progressToken,
		"value": map[string]any{"kind": "begin", "title": "Stvena", "message": "waiting for stvena"},
	})
	s.progress = true
	s.status = "waiting for stvena"
}

func (s *server) endProgress() {
	if !s.progress {
		return
	}
	_ = s.conn.notify("$/progress", map[string]any{
		"token": progressToken, "value": map[string]any{"kind": "end"},
	})
	s.progress = false
}

func (s *server) updateStatus(now time.Time) {
	message := s.statusMessage(now)
	if message == s.status || !s.progress {
		s.status = message
		return
	}
	s.status = message
	_ = s.conn.notify("$/progress", map[string]any{
		"token": progressToken,
		"value": map[string]any{"kind": "report", "message": message},
	})
}

func (s *server) statusMessage(now time.Time) string {
	live := liveState(s.state, now) || liveReview(s.review, now)
	parts := []string{}
	switch {
	case !s.following:
		parts = append(parts, "paused")
	case s.state != nil && s.state.Error != "":
		parts = append(parts, "waiting for capture")
	case live:
		parts = append(parts, "following")
	default:
		parts = append(parts, "waiting for stvena")
	}
	if liveReview(s.review, now) {
		if n := s.review.UnreviewedFiles; n > 0 {
			parts = append(parts, fmt.Sprintf("%d unreviewed", n))
		}
		if pending := s.review.Pending; pending != nil && pending.Count > 0 {
			word := "rejections"
			if pending.Count == 1 {
				word = "rejection"
			}
			switch pending.AppliesAt {
			case "now":
				parts = append(parts, fmt.Sprintf("%d %s applying", pending.Count, word))
			case "manual":
				parts = append(parts, fmt.Sprintf("%d %s waiting for you to apply", pending.Count, word))
			default:
				parts = append(parts, fmt.Sprintf("%d %s waiting for turn end", pending.Count, word))
			}
		}
	}
	return strings.Join(parts, " · ")
}

// jsonNumber keeps command arguments readable in the log without pulling in a
// dependency; it is only used for diagnostics.
func compact(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// renderSignature summarises everything the lens and hint surfaces are computed
// from. Refreshing only when it changes keeps an idle session quiet.
func (s *server) renderSignature() string {
	parts := []string{fmt.Sprintf("follow=%t", s.following)}
	if s.review != nil {
		parts = append(parts, fmt.Sprintf("review=%s:%d:%t", s.review.Session, s.review.Sequence, s.review.Active))
	}
	if s.current != nil {
		parts = append(parts, fmt.Sprintf("marker=%s:%s:%d:%s", s.current.kind, s.current.path, s.current.line, s.current.at))
	}
	keys := make([]string, 0, len(s.optimistic))
	for key, entry := range s.optimistic {
		path, hunkID := splitKey(key)
		keys = append(keys, path+"/"+hunkID+"="+entry.kind)
	}
	sort.Strings(keys)
	return strings.Join(append(parts, keys...), " ")
}

func (s *server) refreshRendered() {
	signature := s.renderSignature()
	if signature == s.rendered {
		return
	}
	s.rendered = signature
	s.refreshLenses()
	s.refreshHints()
}

package editorlsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/editor"
)

// optimisticLifetime bounds a decision this editor has sent but not yet seen
// confirmed. Stvena polls at 700 ms and this server polls again, so a confirmed
// round trip is normally well under a second; anything still outstanding after
// this has been lost rather than delayed. The VS Code extension uses the same
// window, and the two consumers must behave identically.
const optimisticLifetime = 10 * time.Second

// Run serves the Language Server Protocol on stdin/stdout until the editor
// closes the stream or sends exit. Nothing but LSP frames reaches stdout;
// logw (stderr) carries diagnostics.
func Run(args []string, stdin io.Reader, stdout io.Writer, logw io.Writer, version string) error {
	ide := "lsp"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--ide" && i+1 < len(args):
			ide = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--ide="):
			ide = strings.TrimPrefix(args[i], "--ide=")
		default:
			return fmt.Errorf("usage: stvena editor-lsp [--ide NAME]")
		}
	}
	s := &server{
		conn: newConn(stdin, stdout), logw: logw, ide: ide, version: version,
		docs: map[string]*document{}, published: map[string]string{},
		optimistic: map[string]*decision{}, reported: map[string]string{},
		following: true,
	}
	return s.run()
}

// document is one buffer the editor has open. Text is only trusted when the
// editor sends whole-document syncs; see didChange.
type document struct {
	path    string // repository-relative, empty when the file is outside the repo
	text    string
	known   bool
	dirty   bool
	version int
}

// decision is an accept/reject this editor has sent and not yet seen settled.
type decision struct {
	kind      string
	at        time.Time
	requestID string
}

// marker is the location currently being followed, rendered as an inlay hint.
type marker struct {
	kind    string // "edit", "read" or "review"
	path    string
	line    int
	endLine int
	agent   string
	source  string
	// rangeKnown is false for a read the agent reported without a range; the
	// marker then labels the reported line only.
	rangeKnown bool
	at         time.Time
}

type server struct {
	conn    *conn
	logw    io.Writer
	ide     string
	version string

	bridge *bridge
	state  *editor.State
	review *editor.ReviewState

	docs       map[string]*document
	published  map[string]string // uri -> the diagnostics last sent for it
	optimistic map[string]*decision
	reported   map[string]string
	rendered   string // signature of what the lens and hint surfaces draw

	following bool
	current   *marker // transient edit/read marker, expires after 15 s
	latest    *marker // most recent followed location, for stvena.showLatest
	displayed string  // repository-relative path this server last opened

	seenEdit, seenRead, seenReview string
	// canShowDocument records whether the editor handles window/showDocument.
	// Zed 1.19 does not, so follow navigation degrades to markers there.
	canShowDocument bool
	presenceAt      time.Time
	status          string
	progress        bool

	initialized          bool
	navigationReported   bool
	shuttingDown, exited bool
}

func (s *server) run() error {
	incoming := make(chan *message)
	go func() {
		defer close(incoming)
		for {
			msg, err := s.conn.read()
			if err != nil {
				if !errors.Is(err, errParse) {
					if !errors.Is(err, io.EOF) {
						s.logf("transport: %v", err)
					}
					return
				}
				s.logf("dropped an invalid frame: %v", err)
				continue
			}
			incoming <- msg
		}
	}()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-incoming:
			if !ok {
				s.shutdown()
				return nil
			}
			s.handle(msg)
			if s.exited {
				return nil
			}
		case <-ticker.C:
			if s.initialized && !s.shuttingDown {
				s.poll(time.Now())
			}
		}
	}
}

func (s *server) logf(format string, args ...any) {
	if s.logw == nil {
		return
	}
	fmt.Fprintf(s.logw, "stvena editor-lsp: "+format+"\n", args...)
}

// report logs a condition once per distinct message, so a persistent capture
// error does not repeat every 700 ms.
func (s *server) report(key string, message string) {
	if s.reported[key] == message {
		return
	}
	s.reported[key] = message
	s.logf("%s: %s", key, message)
	s.showMessage(2, "Stvena: "+message)
}

func (s *server) clearReport(key string) { delete(s.reported, key) }

func (s *server) handle(msg *message) {
	switch {
	case msg.isResponse():
		// Replies to this server's own requests are advisory; a failure is
		// worth a log line and nothing more.
		if msg.Error != nil {
			s.logf("editor refused a request: %s", msg.Error.Message)
		}
		return
	case msg.isRequest():
		s.handleRequest(msg)
	default:
		s.handleNotification(msg)
	}
}

func (s *server) handleRequest(msg *message) {
	if s.shuttingDown && msg.Method != "shutdown" {
		_ = s.conn.replyError(msg.ID, codeInvalidRequestAfterShutdown, "server is shutting down")
		return
	}
	switch msg.Method {
	case "initialize":
		s.initialize(msg)
	case "shutdown":
		s.shuttingDown = true
		s.shutdown()
		_ = s.conn.reply(msg.ID, nil)
	case "textDocument/codeLens":
		s.replyCodeLens(msg)
	case "textDocument/codeAction":
		s.replyCodeActions(msg)
	case "textDocument/inlayHint":
		s.replyInlayHints(msg)
	case "workspace/executeCommand":
		s.executeCommand(msg)
	default:
		_ = s.conn.replyError(msg.ID, codeMethodNotFound, "unsupported method %q", msg.Method)
	}
}

func (s *server) handleNotification(msg *message) {
	switch msg.Method {
	case "initialized":
		s.startProgress()
		s.poll(time.Now())
	case "exit":
		s.shutdown()
		s.exited = true
	case "textDocument/didOpen":
		var params struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
				Text    string `json:"text"`
			} `json:"textDocument"`
		}
		if s.decode(msg, &params) {
			doc := &document{path: s.relative(params.TextDocument.URI), text: params.TextDocument.Text,
				known: true, version: params.TextDocument.Version}
			doc.dirty = s.differsFromDisk(doc)
			s.docs[params.TextDocument.URI] = doc
			s.refreshDocument(params.TextDocument.URI)
		}
	case "textDocument/didChange":
		var params struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Range *json.RawMessage `json:"range"`
				Text  string           `json:"text"`
			} `json:"contentChanges"`
		}
		if !s.decode(msg, &params) {
			return
		}
		doc := s.docs[params.TextDocument.URI]
		if doc == nil {
			doc = &document{path: s.relative(params.TextDocument.URI)}
			s.docs[params.TextDocument.URI] = doc
		}
		doc.version = params.TextDocument.Version
		for _, change := range params.ContentChanges {
			if change.Range != nil {
				// This server asks for whole-document syncs. An incremental
				// change means the editor ignored that, so the buffer text is
				// no longer known and the file is treated as unsaved until the
				// next save restores certainty.
				doc.known = false
				doc.text = ""
				continue
			}
			doc.text = change.Text
			doc.known = true
		}
		// An external write the editor reloaded arrives as a change with no
		// save. Comparing against disk is what keeps agent-written files
		// followable instead of looking permanently unsaved.
		doc.dirty = !doc.known || s.differsFromDisk(doc)
		s.refreshDocument(params.TextDocument.URI)
	case "textDocument/didSave":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if s.decode(msg, &params) {
			if doc := s.docs[params.TextDocument.URI]; doc != nil {
				doc.dirty = false
			}
			s.refreshDocument(params.TextDocument.URI)
		}
	case "textDocument/didClose":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if s.decode(msg, &params) {
			delete(s.docs, params.TextDocument.URI)
		}
	case "workspace/didChangeConfiguration", "$/setTrace", "$/cancelRequest":
		// Nothing to do; accepted so the editor does not see an error.
	}
}

func (s *server) decode(msg *message, target any) bool {
	if err := json.Unmarshal(msg.Params, target); err != nil {
		s.logf("invalid %s params: %v", msg.Method, err)
		return false
	}
	return true
}

func (s *server) initialize(msg *message) {
	var params struct {
		RootURI          string `json:"rootUri"`
		RootPath         string `json:"rootPath"`
		WorkspaceFolders []struct {
			URI string `json:"uri"`
		} `json:"workspaceFolders"`
		Capabilities struct {
			Window struct {
				ShowDocument *struct {
					Support bool `json:"support"`
				} `json:"showDocument"`
			} `json:"window"`
		} `json:"capabilities"`
	}
	_ = json.Unmarshal(msg.Params, &params)
	root := params.RootPath
	if len(params.WorkspaceFolders) > 0 {
		root = uriToPath(params.WorkspaceFolders[0].URI)
	} else if params.RootURI != "" {
		root = uriToPath(params.RootURI)
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	// An editor that opens a single file makes that file its worktree, so the
	// root it reports is not always a folder. Zed does this for `zed file.go`;
	// resolving the parent is what keeps a lone file in a repository working.
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	// Opening a file is a request the editor has to advertise. An editor that
	// does not answer window/showDocument would reject every navigation, so
	// following becomes markers-only rather than a stream of errors.
	s.canShowDocument = params.Capabilities.Window.ShowDocument != nil &&
		params.Capabilities.Window.ShowDocument.Support

	if found, err := discover(root); err != nil {
		// Without a repository there is nothing to serve, but the editor still
		// gets a well-formed reply: it decides what to do with a server that
		// reports nothing.
		s.logf("no Git repository at %s: %v", root, err)
	} else {
		s.bridge = found
	}

	_ = s.conn.reply(msg.ID, map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": map[string]any{
				"openClose": true,
				// Whole-document syncs. The dirty check compares the buffer
				// against the file on disk, which needs the full text.
				"change": 1,
				"save":   map[string]any{"includeText": false},
			},
			"codeLensProvider":       map[string]any{"resolveProvider": false},
			"codeActionProvider":     map[string]any{"codeActionKinds": []string{codeActionKind}},
			"executeCommandProvider": map[string]any{"commands": commandNames},
			"inlayHintProvider":      true,
		},
		"serverInfo": map[string]any{"name": "stvena", "version": s.version},
	})
	s.initialized = true
}

// shutdown clears everything this server put in the editor and stops claiming
// an editor is connected.
func (s *server) shutdown() {
	if s.bridge != nil {
		withdraw(s.bridge, s.ide)
	}
	for uri := range s.published {
		_ = s.conn.notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": []any{}})
	}
	s.published = map[string]string{}
	s.endProgress()
}

// poll reads both descriptors, reconciles pending decisions, republishes every
// rendered surface, and follows the newest location.
func (s *server) poll(now time.Time) {
	if s.bridge == nil {
		return
	}
	if now.Sub(s.presenceAt) >= presenceInterval {
		if err := announce(s.bridge, s.ide, s.version, now); err != nil {
			s.report("presence", err.Error())
		} else {
			s.presenceAt = now
			s.clearReport("presence")
		}
	}

	state, err := readState(s.bridge)
	if err != nil {
		s.state = nil
		s.report("live state", err.Error())
	} else {
		s.state = state
		s.clearReport("live state")
	}
	review, err := readReview(s.bridge)
	if err != nil {
		s.review = nil
		s.report("review state", err.Error())
	} else {
		s.review = review
		s.clearReport("review state")
	}
	if s.state != nil && s.state.Error != "" {
		s.report("capture", s.state.Error)
	} else {
		s.clearReport("capture")
	}

	// Re-take the clock now that the descriptors are in hand. Stvena stamps a
	// capture and then writes it, so anything on disk was stamped before this
	// read; comparing it against a timestamp taken up to a poll earlier would
	// make a just-written capture look future-dated. Freshness would then drop
	// it — after this poll had already recorded it as seen, so the navigation
	// would be lost for good rather than retried.
	now = time.Now()

	s.reconcile(now)
	follow := s.candidate(now)
	if follow != nil {
		s.latest = follow
	}
	// Read expiry must clear the marker without navigating back to an old edit.
	if follow != nil && follow.kind != "review" && !fresh(follow.at, now) {
		follow = nil
	}
	if follow != nil && follow.kind != "review" {
		s.current = follow
	}
	if s.current != nil && !fresh(s.current.at, now) {
		s.current = nil
	}
	if !s.following {
		s.current = nil
	}

	s.publishDiagnostics(now)
	// Zed re-requests lenses and hints on every refresh, so asking for one on
	// every poll would mean two round trips a second per open buffer for a
	// picture that has not changed.
	s.refreshRendered()
	s.updateStatus(now)
	if s.following && follow != nil {
		s.show(follow, true)
	}
}

// candidate ports the VS Code extension's follow selection: a changed capture,
// a newly reported read, or a moved review focus, newest wins.
func (s *server) candidate(now time.Time) *marker {
	live := liveState(s.state, now) && s.state.Error == ""
	liveRev := liveReview(s.review, now)

	editKey, readID, reviewKey := "", "", ""
	if live {
		editKey = fmt.Sprintf("%s:%d", s.state.Session, s.state.Sequence)
		if s.state.Activity != nil {
			readID = s.state.Activity.ID
		}
	}
	if liveRev {
		reviewKey = fmt.Sprintf("%s:%d", s.review.Session, s.review.Sequence)
	}

	var candidate *marker
	if live {
		read := s.readMarker(now)
		if s.seenEdit != editKey || (read != nil && s.seenRead != readID) {
			if s.seenEdit != editKey {
				candidate = s.editMarker()
			}
			if read != nil && s.seenRead != readID &&
				(candidate == nil || !read.at.Before(candidate.at)) {
				candidate = read
			}
		}
	}
	if liveRev && s.seenReview != reviewKey {
		if review := s.reviewMarker(); review != nil &&
			(candidate == nil || !review.at.Before(candidate.at)) {
			candidate = review
		}
	}
	s.seenEdit, s.seenRead, s.seenReview = editKey, readID, reviewKey
	return candidate
}

func (s *server) editMarker() *marker {
	if s.state == nil {
		return nil
	}
	var first *marker
	for i := range s.state.Files {
		file := &s.state.Files[i]
		// A deleted file has no working file to open, and binary or truncated
		// captures have no complete text preview.
		if file.Binary || file.Truncated || zeroOID.MatchString(file.After) {
			continue
		}
		m := &marker{kind: "edit", path: file.Path, line: file.Line, endLine: file.Line,
			rangeKnown: true, at: s.state.ChangedAt}
		if len(file.Ranges) > 0 {
			m.line, m.endLine = file.Ranges[0].Start, file.Ranges[0].End
		}
		// Prefer the file the user is already looking at, as VS Code does.
		if file.Path == s.displayed {
			return m
		}
		if first == nil {
			first = m
		}
	}
	return first
}

func (s *server) readMarker(now time.Time) *marker {
	if s.state == nil || s.state.Activity == nil {
		return nil
	}
	activity := s.state.Activity
	if !fresh(activity.UpdatedAt, now) {
		return nil
	}
	end, known := activity.EndLine, activity.EndLine >= activity.Line
	if !known {
		end = activity.Line
	}
	return &marker{kind: "read", path: activity.Path, line: activity.Line, endLine: end,
		agent: activity.Agent, rangeKnown: known, at: activity.UpdatedAt}
}

func (s *server) reviewMarker() *marker {
	if s.review == nil || s.review.Focus == nil {
		return nil
	}
	focus := s.review.Focus
	at := s.review.ChangedAt
	if at.IsZero() {
		at = s.review.UpdatedAt
	}
	return &marker{kind: "review", path: focus.Path, line: focus.Line, endLine: focus.EndLine,
		source: focus.Source, rangeKnown: true, at: at}
}

// show opens the ordinary working file at a location without taking focus. It
// never opens a diff tab and never touches an unsaved buffer automatically.
func (s *server) show(m *marker, automatic bool) {
	if s.bridge == nil || m == nil || m.path == "" {
		return
	}
	uri := pathToURI(filepath.Join(s.bridge.root, filepath.FromSlash(m.path)))
	if automatic {
		if doc := s.docs[uri]; doc != nil && doc.dirty {
			return
		}
	}
	if !s.canShowDocument {
		// Say it once, then keep marking: the hints, diagnostics and lenses are
		// still accurate, the editor just will not jump to the location.
		if !s.navigationReported {
			s.navigationReported = true
			s.logf("this editor does not support window/showDocument; Stvena marks locations but cannot open them")
			_ = s.conn.notify("window/logMessage", map[string]any{"type": 3,
				"message": "Stvena: this editor cannot be asked to open a file (no window/showDocument support), " +
					"so changes are marked where they are instead of being opened. The Project Diagnostics panel lists them."})
		}
		s.displayed = m.path
		s.latest = m
		if m.kind != "review" {
			s.current = m
		}
		s.refreshHints()
		return
	}
	start := m.line - 1
	if start < 0 {
		start = 0
	}
	end := m.endLine - 1
	if end < start {
		end = start
	}
	err := s.conn.request("window/showDocument", map[string]any{
		"uri":       uri,
		"external":  false,
		"takeFocus": false,
		"selection": map[string]any{
			"start": map[string]int{"line": start, "character": 0},
			"end":   map[string]int{"line": end, "character": 0},
		},
	})
	if err != nil {
		s.logf("showDocument: %v", err)
		return
	}
	s.displayed = m.path
	s.latest = m
	if m.kind != "review" {
		s.current = m
	}
	s.refreshHints()
}

// differsFromDisk reports whether a buffer no longer matches the file Stvena
// captured. Comparing text rather than trusting save notifications is what lets
// a file the agent wrote, and the editor reloaded, stay followable.
func (s *server) differsFromDisk(doc *document) bool {
	if !doc.known || s.bridge == nil || doc.path == "" {
		return !doc.known
	}
	data, err := os.ReadFile(filepath.Join(s.bridge.root, filepath.FromSlash(doc.path)))
	if err != nil {
		return true
	}
	return string(data) != doc.text
}

// relative maps a document URI to its repository-relative path, or "" when the
// file is outside the repository.
func (s *server) relative(uri string) string {
	if s.bridge == nil {
		return ""
	}
	path := uriToPath(uri)
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(s.bridge.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// refreshDocument re-renders the surfaces that depend on one buffer's state.
// Opening, saving or editing a buffer changes what may be drawn on it, so this
// refreshes unconditionally rather than comparing signatures.
func (s *server) refreshDocument(uri string) {
	s.publishDiagnostics(time.Now())
	s.refreshLenses()
	s.refreshHints()
	s.rendered = s.renderSignature()
}

func uriToPath(uri string) string {
	if uri == "" {
		return ""
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	path := parsed.Path
	// file:///C:/x on Windows-style URIs.
	if len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}

func pathToURI(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

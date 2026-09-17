package editorlsp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nccapo/stvena/internal/editor"
	"github.com/nccapo/stvena/internal/session"
)

const hunkID = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

// client drives the server the way an editor does: one stdio connection, every
// frame parsed, nothing else on the channel.
type client struct {
	t    *testing.T
	conn *conn
	stop func()

	mu     sync.Mutex
	seen   []message
	signal chan struct{}
	nextID int
}

func start(t *testing.T, r *bridge) *client {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	logs := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run([]string{"--ide", "zed"}, serverIn, serverOut, logs, "0.1.0-test")
		_ = serverOut.Close()
	}()

	c := &client{t: t, conn: newConn(clientIn, clientOut), signal: make(chan struct{}, 1), nextID: 1}
	go func() {
		for {
			msg, err := c.conn.read()
			if err != nil {
				return
			}
			c.mu.Lock()
			c.seen = append(c.seen, *msg)
			c.mu.Unlock()
			select {
			case c.signal <- struct{}{}:
			default:
			}
		}
	}()
	c.stop = func() {
		_ = clientOut.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server exited with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not exit after its input closed")
		}
		logged := logs.String()
		if strings.Contains(logged, "transport:") {
			t.Errorf("transport errors in the log:\n%s", logged)
		}
		// The server's own log is the only account of what it decided; a failing
		// test is unreadable without it.
		if t.Failed() && logged != "" {
			t.Logf("server log:\n%s", logged)
		}
	}
	t.Cleanup(c.stop)

	c.call("initialize", map[string]any{"rootUri": pathToURI(r.root),
		"capabilities": map[string]any{"window": map[string]any{"showDocument": map[string]any{"support": true}}}})
	c.notify("initialized", map[string]any{})
	return c
}

func (c *client) notify(method string, params any) {
	c.t.Helper()
	if err := c.conn.notify(method, params); err != nil {
		c.t.Fatalf("notify %s: %v", method, err)
	}
}

// call sends a request and returns the matching response result.
func (c *client) call(method string, params any) json.RawMessage {
	c.t.Helper()
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()
	raw, err := json.Marshal(params)
	if err != nil {
		c.t.Fatalf("marshal %s params: %v", method, err)
	}
	if err := c.conn.send(message{ID: json.RawMessage(itoa(id)), Method: method, Params: raw}); err != nil {
		c.t.Fatalf("send %s: %v", method, err)
	}
	reply := c.await("response to "+method, func(msg message) bool {
		return msg.isResponse() && string(msg.ID) == itoa(id)
	})
	if reply.Error != nil {
		c.t.Fatalf("%s failed: %s", method, reply.Error.Message)
	}
	return reply.Result
}

// await waits for the first frame matching want, scanning frames already
// received so a test never races the server.
func (c *client) await(what string, want func(message) bool) message {
	c.t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		c.mu.Lock()
		for _, msg := range c.seen {
			if want(msg) {
				c.mu.Unlock()
				return msg
			}
		}
		c.mu.Unlock()
		select {
		case <-c.signal:
		case <-deadline:
			c.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (c *client) awaitMethod(method string) message {
	c.t.Helper()
	return c.await(method, func(msg message) bool { return msg.Method == method })
}

// forget drops the frames seen so far, so a later await cannot match an older
// one from a previous step.
func (c *client) forget() {
	c.mu.Lock()
	c.seen = nil
	c.mu.Unlock()
}

type syncBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// project builds a repository with one committed source file.
func project(t *testing.T) (*bridge, string) {
	t.Helper()
	r := testRepo(t)
	source := "package main\n\nfunc main() {\n\tprintln(\"one\")\n\tprintln(\"two\")\n\tprintln(\"three\")\n}\n"
	write(t, filepath.Join(r.root, "app.go"), source)
	run(t, r.root, "add", ".")
	run(t, r.root, "commit", "-qm", "baseline")
	return r, source
}

func publishReview(t *testing.T, r *bridge, state editor.ReviewState) {
	t.Helper()
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Session == "" {
		state.Session = "review-session"
	}
	state.Active = true
	state.UpdatedAt = time.Now().UTC()
	if state.ChangedAt.IsZero() {
		state.ChangedAt = state.UpdatedAt
	}
	if err := session.AtomicJSON(r.reviewPath, state); err != nil {
		t.Fatalf("publish review: %v", err)
	}
}

func reviewWithHunk(reviewed, rejected bool) editor.ReviewState {
	return editor.ReviewState{
		Sequence: 1, UnreviewedFiles: 1, UnreviewedHunks: 1, Features: editor.Features,
		Tree: strings.Repeat("1", 40),
		Files: []editor.ReviewFile{{Path: "app.go", Status: "M", Hunks: []editor.ReviewHunk{
			{ID: hunkID, Start: 4, End: 5, Reviewed: reviewed, Rejected: rejected},
		}}},
	}
}

func lensTitles(t *testing.T, c *client, r *bridge) []string {
	t.Helper()
	raw := c.call("textDocument/codeLens", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(filepath.Join(r.root, "app.go"))},
	})
	var lenses []struct {
		Range   lspRange `json:"range"`
		Command command  `json:"command"`
	}
	if err := json.Unmarshal(raw, &lenses); err != nil {
		t.Fatalf("decode lenses: %v", err)
	}
	titles := make([]string, 0, len(lenses))
	for _, lens := range lenses {
		titles = append(titles, lens.Command.Title)
	}
	return titles
}

// awaitLenses waits for the lens surface to settle on a count. A descriptor
// lands on the server's next poll, so tests wait for the drawn result rather
// than for a refresh notification, which the server also sends for other
// reasons.
func awaitLenses(t *testing.T, c *client, r *bridge, want int) []string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		titles := lensTitles(t, c, r)
		if len(titles) == want {
			return titles
		}
		if time.Now().After(deadline) {
			t.Fatalf("lens titles = %v, want %d of them", titles, want)
			return titles
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// codeActions asks for the actions on one line, which is also the probe for
// "the server has a live review descriptor": review is the one action every
// Stvena build advertises.
func codeActions(t *testing.T, c *client, r *bridge, line int) map[string]string {
	t.Helper()
	raw := c.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(filepath.Join(r.root, "app.go"))},
		"range":        lspRange{Start: position{Line: line}, End: position{Line: line}},
		"context":      map[string]any{"diagnostics": []any{}},
	})
	var actions []struct {
		Title   string  `json:"title"`
		Kind    string  `json:"kind"`
		Command command `json:"command"`
	}
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatalf("decode actions: %v", err)
	}
	offered := map[string]string{}
	for _, action := range actions {
		if action.Kind != codeActionKind {
			t.Fatalf("action %q has kind %q, want %s", action.Title, action.Kind, codeActionKind)
		}
		offered[action.Command.Command] = action.Title
	}
	return offered
}

func awaitLiveReview(t *testing.T, c *client, r *bridge) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, ok := codeActions(t, c, r, 3)["stvena.review"]; ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never picked up the review descriptor")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func openFile(c *client, r *bridge, text string) string {
	uri := pathToURI(filepath.Join(r.root, "app.go"))
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": "go", "version": 1, "text": text}})
	return uri
}

func TestInitializeAdvertisesTheSurfaceZedRenders(t *testing.T) {
	r, _ := project(t)
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run([]string{"--ide", "zed"}, serverIn, serverOut, io.Discard, "9.9.9") }()
	c := &client{t: t, conn: newConn(clientIn, clientOut), signal: make(chan struct{}, 1), nextID: 1}
	go func() {
		for {
			msg, err := c.conn.read()
			if err != nil {
				return
			}
			c.mu.Lock()
			c.seen = append(c.seen, *msg)
			c.mu.Unlock()
			select {
			case c.signal <- struct{}{}:
			default:
			}
		}
	}()

	raw := c.call("initialize", map[string]any{"rootUri": pathToURI(r.root)})
	var result struct {
		Capabilities struct {
			TextDocumentSync struct {
				OpenClose bool `json:"openClose"`
				Change    int  `json:"change"`
				Save      struct {
					IncludeText bool `json:"includeText"`
				} `json:"save"`
			} `json:"textDocumentSync"`
			CodeLensProvider struct {
				ResolveProvider bool `json:"resolveProvider"`
			} `json:"codeLensProvider"`
			CodeActionProvider struct {
				CodeActionKinds []string `json:"codeActionKinds"`
			} `json:"codeActionProvider"`
			ExecuteCommandProvider struct {
				Commands []string `json:"commands"`
			} `json:"executeCommandProvider"`
			InlayHintProvider bool `json:"inlayHintProvider"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	caps := result.Capabilities
	if !caps.TextDocumentSync.OpenClose || caps.TextDocumentSync.Change == 0 || caps.TextDocumentSync.Save.IncludeText {
		t.Fatalf("text sync = %+v, want open/close with saves and no text", caps.TextDocumentSync)
	}
	if caps.CodeLensProvider.ResolveProvider {
		t.Fatal("lenses are published complete; resolveProvider must be false")
	}
	if len(caps.CodeActionProvider.CodeActionKinds) != 1 || caps.CodeActionProvider.CodeActionKinds[0] != codeActionKind {
		t.Fatalf("code action kinds = %v, want [%s]", caps.CodeActionProvider.CodeActionKinds, codeActionKind)
	}
	if !caps.InlayHintProvider {
		t.Fatal("inlay hints carry the markers; the provider must be advertised")
	}
	if len(caps.ExecuteCommandProvider.Commands) != len(commandNames) {
		t.Fatalf("commands = %v, want %v", caps.ExecuteCommandProvider.Commands, commandNames)
	}
	if result.ServerInfo.Name != "stvena" || result.ServerInfo.Version != "9.9.9" {
		t.Fatalf("serverInfo = %+v, want the stvena build version", result.ServerInfo)
	}

	// shutdown then exit is the ordinary lifecycle; the process must end.
	c.call("shutdown", nil)
	c.notify("exit", nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after the exit notification")
	}
}

func TestPresenceAppearsSoAfterConnecting(t *testing.T) {
	r, _ := project(t)
	start(t, r)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if name := editor.ReadPresence(r.presencePath); name == "zed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("stvena-ide.json never announced this editor")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnreviewedHunksBecomeLensesAndDiagnostics(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))

	published := c.await("diagnostics for app.go", func(msg message) bool {
		return msg.Method == "textDocument/publishDiagnostics" &&
			strings.Contains(string(msg.Params), "app.go") &&
			strings.Contains(string(msg.Params), "unreviewed")
	})
	var diagnostics struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range    lspRange `json:"range"`
			Severity int      `json:"severity"`
			Source   string   `json:"source"`
			Message  string   `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(published.Params, &diagnostics); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if len(diagnostics.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one unreviewed item", diagnostics.Diagnostics)
	}
	item := diagnostics.Diagnostics[0]
	if item.Severity != 3 || item.Source != "stvena" || item.Range.Start.Line != 3 {
		t.Fatalf("diagnostic = %+v, want Information on the hunk's first line", item)
	}
	c.awaitMethod("workspace/codeLens/refresh")

	titles := awaitLenses(t, c, r, 2)
	if !strings.Contains(titles[0], "Accept") || !strings.Contains(titles[1], "Reject") {
		t.Fatalf("lens titles = %v, want accept and reject", titles)
	}
}

func TestAcceptWritesARequestAndFlipsTheLensImmediately(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))
	awaitLenses(t, c, r, 2)

	c.call("workspace/executeCommand", map[string]any{
		"command":   "stvena.accept",
		"arguments": []any{map[string]any{"path": "app.go", "hunkId": hunkID, "line": 4, "endLine": 5}},
	})

	var request editor.Request
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(r.requestPath)
		if err == nil && json.Unmarshal(data, &request) == nil && request.Action == "accept" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no accept request was written (last error %v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if request.Version != 1 || request.Session != "review-session" || request.HunkID != hunkID ||
		request.Path != "app.go" || request.Line != 4 || request.EndLine != 5 || request.ID == "" {
		t.Fatalf("request = %+v, want a session-bound accept naming the hunk", request)
	}
	if time.Since(request.UpdatedAt) > time.Minute {
		t.Fatalf("request timestamp %s is outside Stvena's freshness window", request.UpdatedAt)
	}

	// The decision shows before Stvena confirms it, or the buttons look broken.
	titles := lensTitles(t, c, r)
	if len(titles) != 1 || !strings.Contains(titles[0], "Accepted") || !strings.Contains(titles[0], "…") {
		t.Fatalf("lens titles = %v, want a pending accepted state", titles)
	}
	_ = titles

	// Stvena agrees: the pending marker goes away and the state stays.
	publishReview(t, r, reviewWithHunk(true, false))
	deadline = time.Now().Add(5 * time.Second)
	for {
		titles = lensTitles(t, c, r)
		if len(titles) == 1 && strings.Contains(titles[0], "Accepted") && !strings.Contains(titles[0], "…") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lens titles = %v, want a settled accepted state", titles)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRefusedRequestRollsTheLensBack(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))
	awaitLenses(t, c, r, 2)
	c.call("workspace/executeCommand", map[string]any{
		"command":   "stvena.reject",
		"arguments": []any{map[string]any{"path": "app.go", "hunkId": hunkID, "line": 4, "endLine": 5}},
	})
	var request editor.Request
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(r.requestPath)
		if err == nil && json.Unmarshal(data, &request) == nil && request.Action == "reject" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no reject request was written")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.forget()

	refused := reviewWithHunk(false, false)
	refused.Sequence = 2
	refused.LastRequest = &editor.RequestResult{ID: request.ID, Action: "reject", Status: "refused",
		Message: "that change block has moved", At: time.Now().UTC()}
	publishReview(t, r, refused)

	c.await("the refusal message", func(msg message) bool {
		return msg.Method == "window/showMessage" && strings.Contains(string(msg.Params), "has moved")
	})
	deadline = time.Now().Add(5 * time.Second)
	for {
		titles := lensTitles(t, c, r)
		if len(titles) == 2 && strings.Contains(titles[0], "✓ Accept") && !strings.Contains(titles[0], "…") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lens titles = %v, want the rejection rolled back", titles)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnsavedBufferSuppressesEverythingUntilItMatchesDisk(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	uri := openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))
	awaitLenses(t, c, r, 2)

	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []any{map[string]any{"text": source + "// edited\n"}},
	})
	if titles := lensTitles(t, c, r); len(titles) != 0 {
		t.Fatalf("lens titles on an unsaved buffer = %v, want none", titles)
	}
	raw := c.call("textDocument/inlayHint", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range":        lspRange{Start: position{}, End: position{Line: 100}},
	})
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("hints on an unsaved buffer = %s, want none", raw)
	}
	for name, title := range codeActions(t, c, r, 3) {
		if name != "stvena.toggleFollow" {
			t.Fatalf("located action %q (%s) offered on an unsaved buffer", title, name)
		}
	}

	// Saving restores everything.
	c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}})
	awaitLenses(t, c, r, 2)
}

// The agent writes the file and the editor reloads it: that arrives as a change
// with no save. Comparing the buffer against disk is what keeps the file
// followable instead of looking permanently unsaved.
func TestExternalWriteReloadStaysFollowable(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	uri := openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))
	awaitLenses(t, c, r, 2)

	agentWritten := strings.Replace(source, "\"two\"", "\"TWO\"", 1)
	write(t, filepath.Join(r.root, "app.go"), agentWritten)
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 3},
		"contentChanges": []any{map[string]any{"text": agentWritten}},
	})
	if titles := lensTitles(t, c, r); len(titles) != 2 {
		t.Fatalf("lens titles after an external write = %v, want them kept", titles)
	}
}

func TestFollowOpensTheWorkingFileWithoutTakingFocus(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	c.forget()

	if err := session.AtomicJSON(r.statePath, editor.State{
		Version: 1, Session: "live-session", Sequence: 4, Active: true,
		UpdatedAt: time.Now().UTC(), ChangedAt: time.Now().UTC(),
		Files: []editor.Change{{Path: "app.go", Status: "M", Before: strings.Repeat("1", 40),
			After: strings.Repeat("2", 40), Line: 5, Added: 1, Deleted: 1,
			Ranges: []editor.LineRange{{Start: 5, End: 5}}}},
	}); err != nil {
		t.Fatalf("publish live state: %v", err)
	}

	shown := c.await("window/showDocument", func(msg message) bool { return msg.Method == "window/showDocument" })
	var params struct {
		URI       string   `json:"uri"`
		External  bool     `json:"external"`
		TakeFocus bool     `json:"takeFocus"`
		Selection lspRange `json:"selection"`
	}
	if err := json.Unmarshal(shown.Params, &params); err != nil {
		t.Fatalf("decode showDocument: %v", err)
	}
	if params.External || params.TakeFocus {
		t.Fatalf("showDocument = %+v, want the terminal to keep focus and no external open", params)
	}
	if !strings.HasSuffix(params.URI, "/app.go") || params.Selection.Start.Line != 4 {
		t.Fatalf("showDocument = %+v, want app.go selected at the changed line", params)
	}

	// The edit marker is an inlay hint on that line while it is fresh.
	raw := c.call("textDocument/inlayHint", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(filepath.Join(r.root, "app.go"))},
		"range":        lspRange{Start: position{}, End: position{Line: 100}},
	})
	var hints []struct {
		Position position `json:"position"`
		Label    string   `json:"label"`
	}
	if err := json.Unmarshal(raw, &hints); err != nil {
		t.Fatalf("decode hints: %v", err)
	}
	if len(hints) != 1 || hints[0].Position.Line != 4 || !strings.Contains(hints[0].Label, "agent edit") {
		t.Fatalf("hints = %+v, want one edit marker on the changed line", hints)
	}
}

func TestReportedReadIsMarkedAndExpires(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)

	// A read reported 16 seconds ago has already expired: it must neither
	// navigate nor mark, and it must never pull the editor back to old work.
	stale := editor.State{Version: 1, Session: "live-session", Sequence: 1, Active: true,
		UpdatedAt: time.Now().UTC(),
		Activity: &editor.Activity{Session: "live-session", ID: "read-1", Agent: "claude",
			Path: "app.go", Line: 3, EndLine: 6, UpdatedAt: time.Now().UTC().Add(-16 * time.Second)}}
	if err := session.AtomicJSON(r.statePath, stale); err != nil {
		t.Fatalf("publish stale read: %v", err)
	}
	time.Sleep(2 * pollInterval)
	raw := c.call("textDocument/inlayHint", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(filepath.Join(r.root, "app.go"))},
		"range":        lspRange{Start: position{}, End: position{Line: 100}},
	})
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("hints for an expired read = %s, want none", raw)
	}

	fresh := stale
	fresh.Sequence = 2
	fresh.UpdatedAt = time.Now().UTC()
	fresh.Activity = &editor.Activity{Session: "live-session", ID: "read-2", Agent: "claude",
		Path: "app.go", Line: 3, EndLine: 6, UpdatedAt: time.Now().UTC()}
	if err := session.AtomicJSON(r.statePath, fresh); err != nil {
		t.Fatalf("publish fresh read: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw = c.call("textDocument/inlayHint", map[string]any{
			"textDocument": map[string]any{"uri": pathToURI(filepath.Join(r.root, "app.go"))},
			"range":        lspRange{Start: position{}, End: position{Line: 100}},
		})
		if strings.Contains(string(raw), "read (claude)") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hints = %s, want the read marked with its agent", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestActionsAndLensesAreGatedOnAdvertisedFeatures(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)

	// An older Stvena advertises nothing: only review and context are safe.
	old := reviewWithHunk(false, false)
	old.Features = nil
	publishReview(t, r, old)
	awaitLiveReview(t, c, r)
	if titles := lensTitles(t, c, r); len(titles) != 0 {
		t.Fatalf("lens titles against an older Stvena = %v, want none", titles)
	}
	offered := codeActions(t, c, r, 3)
	if _, ok := offered["stvena.context"]; !ok {
		t.Fatalf("actions = %v, want review and context", offered)
	}
	if _, ok := offered["stvena.acceptFile"]; ok {
		t.Fatalf("actions = %v, want nothing the descriptor does not advertise", offered)
	}
	if _, ok := offered["stvena.prompt"]; ok {
		t.Fatalf("actions = %v, want nothing the descriptor does not advertise", offered)
	}

	// A refused action must not reach the descriptor at all.
	os.Remove(r.requestPath)
	c.call("workspace/executeCommand", map[string]any{
		"command":   "stvena.accept",
		"arguments": []any{map[string]any{"path": "app.go", "hunkId": hunkID, "line": 4, "endLine": 5}},
	})
	c.await("the refusal", func(msg message) bool {
		return msg.Method == "window/showMessage" && strings.Contains(string(msg.Params), "does not support")
	})
	if _, err := os.Stat(r.requestPath); !os.IsNotExist(err) {
		t.Fatalf("a request was written for an unsupported action (stat err %v)", err)
	}
}

func TestAddedFilesOfferRejectFileOnly(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	added := reviewWithHunk(false, false)
	added.Files[0].Status = "A"
	publishReview(t, r, added)
	titles := awaitLenses(t, c, r, 2)
	if !strings.Contains(titles[1], "Reject file") {
		t.Fatalf("lens titles for an added file = %v, want a whole-file rejection", titles)
	}
}

func TestPauseStopsFollowingAndResumeRestoresIt(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))
	awaitLenses(t, c, r, 2)

	c.call("workspace/executeCommand", map[string]any{"command": "stvena.toggleFollow"})
	c.forget()
	if err := session.AtomicJSON(r.statePath, editor.State{
		Version: 1, Session: "live-session", Sequence: 9, Active: true,
		UpdatedAt: time.Now().UTC(), ChangedAt: time.Now().UTC(),
		Files: []editor.Change{{Path: "app.go", Status: "M", Before: strings.Repeat("1", 40),
			After: strings.Repeat("2", 40), Line: 6, Added: 1, Deleted: 0}},
	}); err != nil {
		t.Fatalf("publish live state: %v", err)
	}
	time.Sleep(3 * pollInterval)
	c.mu.Lock()
	for _, msg := range c.seen {
		if msg.Method == "window/showDocument" {
			c.mu.Unlock()
			t.Fatal("a paused server navigated the editor")
		}
	}
	c.mu.Unlock()

	// Resuming shows the location that was missed.
	c.call("workspace/executeCommand", map[string]any{"command": "stvena.toggleFollow"})
	c.await("window/showDocument after resuming", func(msg message) bool { return msg.Method == "window/showDocument" })
}

func TestShutdownClearsDiagnosticsAndPresence(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	publishReview(t, r, reviewWithHunk(false, false))
	c.await("diagnostics", func(msg message) bool {
		return msg.Method == "textDocument/publishDiagnostics" && strings.Contains(string(msg.Params), "unreviewed")
	})
	if editor.ReadPresence(r.presencePath) != "zed" {
		t.Fatal("presence should be announced before shutdown")
	}
	c.forget()

	c.call("shutdown", nil)
	cleared := c.await("cleared diagnostics", func(msg message) bool {
		return msg.Method == "textDocument/publishDiagnostics"
	})
	if !strings.Contains(string(cleared.Params), `"diagnostics":[]`) {
		t.Fatalf("shutdown published %s, want an empty list", cleared.Params)
	}
	if name := editor.ReadPresence(r.presencePath); name != "" {
		t.Fatalf("presence after shutdown = %q, want it withdrawn", name)
	}
}

// The status item is the only persistent surface Zed gives a language server.
func TestStatusReportsFollowingAndPendingRejections(t *testing.T) {
	r, source := project(t)
	c := start(t, r)
	openFile(c, r, source)
	state := reviewWithHunk(false, false)
	state.UnreviewedFiles = 3
	state.Pending = &editor.PendingRejections{Count: 1, AppliesAt: "turn-end", Reason: "claude 1 is running"}
	publishReview(t, r, state)

	progress := c.await("a progress report", func(msg message) bool {
		return msg.Method == "$/progress" && strings.Contains(string(msg.Params), "report")
	})
	var report struct {
		Token string `json:"token"`
		Value struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"value"`
	}
	if err := json.Unmarshal(progress.Params, &report); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if report.Token != progressToken {
		t.Fatalf("progress token = %q, want the long-lived %q", report.Token, progressToken)
	}
	for _, want := range []string{"following", "3 unreviewed", "1 rejection waiting for turn end"} {
		if !strings.Contains(report.Value.Message, want) {
			t.Fatalf("status = %q, want it to mention %q", report.Value.Message, want)
		}
	}
}

// TestMain prints the same marker the VS Code host suite prints, so a wrapper
// script can check for a reported pass rather than an exit status.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		fmt.Println("STVENA_LSP_TESTS_PASSED")
	}
	os.Exit(code)
}

// Zed makes a single opened file its own worktree, so the root in initialize is
// sometimes a file. Resolving its folder is what keeps `zed app.go` working.
func TestInitializeAcceptsAFileAsTheRoot(t *testing.T) {
	r, _ := project(t)
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	logs := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Run([]string{"--ide", "zed"}, serverIn, serverOut, logs, "0.1.0-test") }()
	c := &client{t: t, conn: newConn(clientIn, clientOut), signal: make(chan struct{}, 1), nextID: 1}
	go func() {
		for {
			msg, err := c.conn.read()
			if err != nil {
				return
			}
			c.mu.Lock()
			c.seen = append(c.seen, *msg)
			c.mu.Unlock()
			select {
			case c.signal <- struct{}{}:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = clientOut.Close()
		<-done
	})

	c.call("initialize", map[string]any{
		"rootUri":      pathToURI(filepath.Join(r.root, "app.go")),
		"capabilities": map[string]any{},
	})
	c.notify("initialized", map[string]any{})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if editor.ReadPresence(r.presencePath) == "zed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a file root never resolved to its repository; server log:\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An editor that does not answer window/showDocument (Zed 1.19 does not) must
// still get every marker; it just never has a file opened for it, and it is
// told why once rather than on every capture.
func TestEditorWithoutShowDocumentStillGetsMarkers(t *testing.T) {
	r, source := project(t)
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Run([]string{"--ide", "zed"}, serverIn, serverOut, io.Discard, "0.1.0-test") }()
	c := &client{t: t, conn: newConn(clientIn, clientOut), signal: make(chan struct{}, 1), nextID: 1}
	go func() {
		for {
			msg, err := c.conn.read()
			if err != nil {
				return
			}
			c.mu.Lock()
			c.seen = append(c.seen, *msg)
			c.mu.Unlock()
			select {
			case c.signal <- struct{}{}:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = clientOut.Close()
		<-done
	})
	// No window.showDocument in the client capabilities.
	c.call("initialize", map[string]any{"rootUri": pathToURI(r.root), "capabilities": map[string]any{}})
	c.notify("initialized", map[string]any{})
	openFile(c, r, source)

	if err := session.AtomicJSON(r.statePath, editor.State{
		Version: 1, Session: "live-session", Sequence: 4, Active: true,
		UpdatedAt: time.Now().UTC(), ChangedAt: time.Now().UTC(),
		Files: []editor.Change{{Path: "app.go", Status: "M", Before: strings.Repeat("1", 40),
			After: strings.Repeat("2", 40), Line: 5, Added: 1, Deleted: 0,
			Ranges: []editor.LineRange{{Start: 5, End: 5}}}},
	}); err != nil {
		t.Fatalf("publish live state: %v", err)
	}

	c.await("the explanation", func(msg message) bool {
		return msg.Method == "window/logMessage" && strings.Contains(string(msg.Params), "window/showDocument")
	})
	c.mu.Lock()
	explanations := 0
	for _, msg := range c.seen {
		if msg.Method == "window/showDocument" {
			c.mu.Unlock()
			t.Fatal("asked an editor that cannot answer it to open a document")
		}
		if msg.Method == "window/logMessage" && strings.Contains(string(msg.Params), "window/showDocument") {
			explanations++
		}
	}
	c.mu.Unlock()
	if explanations != 1 {
		t.Fatalf("explained %d times, want exactly once", explanations)
	}

	// The marker is still drawn where the change is.
	raw := c.call("textDocument/inlayHint", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(filepath.Join(r.root, "app.go"))},
		"range":        lspRange{Start: position{}, End: position{Line: 100}},
	})
	if !strings.Contains(string(raw), "agent edit") {
		t.Fatalf("hints = %s, want the edit still marked", raw)
	}
}

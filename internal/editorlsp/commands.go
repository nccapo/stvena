package editorlsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nccapo/stvena/internal/editor"
)

// decisionActions map a lens or action command onto the protocol action it
// writes, and onto the optimistic state the editor shows until Stvena agrees.
var decisionActions = map[string]string{
	"stvena.accept":          "accept",
	"stvena.unaccept":        "unaccept",
	"stvena.reject":          "reject",
	"stvena.undoReject":      "undo-reject",
	"stvena.acceptFile":      "accept",
	"stvena.rejectFile":      "reject",
	"stvena.applyRejections": "apply-rejections",
}

func (s *server) executeCommand(msg *message) {
	var params struct {
		Command   string            `json:"command"`
		Arguments []json.RawMessage `json:"arguments"`
	}
	if !s.decode(msg, &params) {
		_ = s.conn.replyError(msg.ID, codeInvalidParams, "invalid executeCommand parameters")
		return
	}
	var ref target
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments[0], &ref); err != nil {
			s.logf("command %s: invalid argument %s", params.Command, compact(string(params.Arguments[0])))
		}
	}
	// Reply first: the editor should not wait on a descriptor write, and every
	// outcome reaches the user through the lens state or a message.
	_ = s.conn.reply(msg.ID, nil)

	now := time.Now()
	switch params.Command {
	case "stvena.toggleFollow":
		s.following = !s.following
		if !s.following {
			s.current = nil
		}
		s.refreshHints()
		s.updateStatus(now)
		if s.following && s.latest != nil {
			s.show(s.latest, true)
		}
		return
	case "stvena.showLatest":
		if s.latest == nil {
			s.showMessage(3, "Stvena: run stvena in this project and wait for a read or captured edit.")
			return
		}
		s.show(s.latest, false)
		return
	case "stvena.review", "stvena.context", "stvena.prompt", "stvena.nextUnreviewed":
		s.sendRequest(params.Command, ref, now)
		return
	}
	action, ok := decisionActions[params.Command]
	if !ok {
		s.showMessage(1, fmt.Sprintf("Stvena: unknown command %q.", params.Command))
		return
	}
	// A file-level decision carries no hunk id: it applies to the whole file.
	if params.Command == "stvena.acceptFile" || params.Command == "stvena.rejectFile" {
		ref = target{Path: ref.Path, Line: 1, EndLine: 1}
	}
	s.decide(action, ref, now)
}

// sendRequest writes a located request that carries no optimistic state.
func (s *server) sendRequest(name string, ref target, now time.Time) {
	action := map[string]string{
		"stvena.review":         "review",
		"stvena.context":        "context",
		"stvena.prompt":         "prompt",
		"stvena.nextUnreviewed": "next-unreviewed",
	}[name]
	if action != "next-unreviewed" && !s.usable(ref.Path) {
		return
	}
	request := editor.Request{Action: action, Path: ref.Path, Line: ref.Line, EndLine: ref.EndLine, Text: ref.Text}
	if _, err := s.write(request, now); err != nil {
		s.showMessage(1, "Stvena: "+err.Error())
		return
	}
	switch action {
	case "review":
		s.showMessage(3, fmt.Sprintf("Stvena: sent %s:%d to review.", ref.Path, ref.Line))
	case "context":
		s.showMessage(3, fmt.Sprintf("Stvena: sent %s:%d–%d to the context tray.", ref.Path, ref.Line, ref.EndLine))
	case "prompt":
		// LSP has no text prompt, so the range goes to the agent's input with an
		// empty question for the user to finish and send.
		s.showMessage(3, "Stvena: the selected range is in the agent's input · type your question there and press Enter.")
	case "next-unreviewed":
		s.showMessage(3, "Stvena: moved to the next unreviewed file.")
	}
}

// decide records the intent immediately so the editor responds at once, then
// writes the request. Stvena is polled, so a confirmed round trip takes about a
// second; without this the buttons would look broken.
func (s *server) decide(action string, ref target, now time.Time) {
	located := !pathlessActions[action]
	if located && !s.usable(ref.Path) {
		return
	}
	key := decisionKey(ref.Path, ref.HunkID)
	s.optimistic[key] = &decision{kind: action, at: now}
	s.refreshLenses()
	s.publishDiagnostics(now)

	request := editor.Request{Action: action, Path: ref.Path, Line: ref.Line, EndLine: ref.EndLine,
		HunkID: ref.HunkID, Text: ref.Text}
	written, err := s.write(request, now)
	if err != nil {
		delete(s.optimistic, key)
		s.refreshLenses()
		s.publishDiagnostics(now)
		s.showMessage(1, "Stvena: "+err.Error())
		return
	}
	if entry := s.optimistic[key]; entry != nil && entry.kind == action {
		entry.requestID = written.ID
	}
}

func (s *server) write(request editor.Request, now time.Time) (editor.Request, error) {
	if s.bridge == nil {
		return request, fmt.Errorf("no Stvena review owns this file")
	}
	// A hunk that is no longer in the descriptor changed underneath the editor;
	// Stvena would refuse the request anyway, so say so without writing.
	if request.HunkID != "" && !s.knownHunk(request.Path, request.HunkID) {
		return request, fmt.Errorf("that change block has moved; reopen the file and try again")
	}
	return writeRequest(s.bridge, s.review, request, now)
}

func (s *server) knownHunk(path, hunkID string) bool {
	file := s.reviewFile(path)
	if file == nil {
		return false
	}
	for i := range file.Hunks {
		if file.Hunks[i].ID == hunkID {
			return true
		}
	}
	return false
}

// usable refuses to act on a buffer the user has edited: the captured lines no
// longer describe it.
func (s *server) usable(path string) bool {
	if path == "" {
		s.showMessage(1, "Stvena: open a repository file first.")
		return false
	}
	if s.dirty(path) {
		s.showMessage(2, "Stvena: save the file before sending its captured range to Stvena.")
		return false
	}
	return true
}

func (s *server) dirty(path string) bool {
	if s.bridge == nil {
		return false
	}
	uri := pathToURI(filepath.Join(s.bridge.root, filepath.FromSlash(path)))
	doc := s.docs[uri]
	return doc != nil && doc.dirty
}

// reconcile drops optimistic decisions Stvena has confirmed, refused, or never
// acted on. Anything left would keep showing a state the user does not have.
func (s *server) reconcile(now time.Time) {
	var last *editor.RequestResult
	if s.review != nil {
		last = s.review.LastRequest
	}
	for key, entry := range s.optimistic {
		path, hunkID := splitKey(key)
		if entry.requestID != "" && last != nil && last.ID == entry.requestID {
			if last.Status == "refused" {
				delete(s.optimistic, key)
				message := last.Message
				if message == "" {
					message = "that change could not be updated."
				}
				s.showMessage(2, "Stvena: "+message)
				continue
			}
			if entry.kind == "apply-rejections" && last.Status == "applied" {
				delete(s.optimistic, key)
				if last.Message != "" {
					s.showMessage(3, "Stvena: "+last.Message)
				}
				continue
			}
		}
		if file := s.reviewFile(path); file != nil {
			reviewed, rejected := file.Reviewed, file.Rejected
			found := hunkID == ""
			if hunkID != "" {
				for i := range file.Hunks {
					if file.Hunks[i].ID == hunkID {
						reviewed, rejected, found = file.Hunks[i].Reviewed, file.Hunks[i].Rejected, true
						break
					}
				}
			}
			settled := found && ((entry.kind == "accept" && reviewed) ||
				(entry.kind == "unaccept" && !reviewed) ||
				(entry.kind == "reject" && rejected) ||
				(entry.kind == "undo-reject" && !rejected))
			if settled {
				delete(s.optimistic, key)
				continue
			}
		}
		// A hunk that vanished from the descriptor changed underneath the editor.
		if now.Sub(entry.at) > optimisticLifetime {
			delete(s.optimistic, key)
		}
	}
}

func splitKey(key string) (path, hunkID string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

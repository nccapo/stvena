package editorlsp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestReadFramesInSequence(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	second := `{"jsonrpc":"2.0","method":"initialized","params":{}}`
	stream := frame(body) + frame(second)
	c := newConn(strings.NewReader(stream), io.Discard)

	first, err := c.read()
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if !first.isRequest() || first.Method != "initialize" {
		t.Fatalf("first frame = %+v, want an initialize request", first)
	}
	next, err := c.read()
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if next.isRequest() || next.Method != "initialized" {
		t.Fatalf("second frame = %+v, want an initialized notification", next)
	}
	if _, err := c.read(); err != io.EOF {
		t.Fatalf("third read = %v, want io.EOF", err)
	}
}

func TestReadRejectsBadFrames(t *testing.T) {
	for name, stream := range map[string]string{
		"no content length": "Content-Type: application/json\r\n\r\n{}",
		"bad length":        "Content-Length: nope\r\n\r\n{}",
		"malformed header":  "not-a-header\r\n\r\n{}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newConn(strings.NewReader(stream), io.Discard).read(); err == nil {
				t.Fatal("read accepted a malformed frame")
			}
		})
	}
}

// An invalid body is reported as errParse so the read loop can skip the frame
// and keep serving: the stream position is still correct.
func TestReadReportsInvalidJSONAsParseError(t *testing.T) {
	_, err := newConn(strings.NewReader(frame("{not json")), io.Discard).read()
	if err == nil || !strings.Contains(err.Error(), errParse.Error()) {
		t.Fatalf("err = %v, want a parse error", err)
	}
}

func TestWriteFramesWithContentLength(t *testing.T) {
	var out bytes.Buffer
	c := newConn(strings.NewReader(""), &out)
	if err := c.notify("window/logMessage", map[string]any{"type": 3, "message": "hi"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	header, body, ok := strings.Cut(out.String(), "\r\n\r\n")
	if !ok {
		t.Fatalf("output has no header separator: %q", out.String())
	}
	if want := "Content-Length: " + itoa(len(body)); header != want {
		t.Fatalf("header = %q, want %q", header, want)
	}
	var msg message
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if msg.JSONRPC != "2.0" || msg.Method != "window/logMessage" || len(msg.ID) != 0 {
		t.Fatalf("body = %+v, want a 2.0 notification", msg)
	}
}

// Server-to-client requests must carry distinct ids, or the editor cannot match
// its replies.
func TestRequestIDsAreUnique(t *testing.T) {
	var out bytes.Buffer
	c := newConn(strings.NewReader(""), &out)
	for i := 0; i < 3; i++ {
		if err := c.request("workspace/codeLens/refresh", nil); err != nil {
			t.Fatalf("request: %v", err)
		}
	}
	seen := map[string]bool{}
	for _, msg := range decodeAll(t, out.String()) {
		id := string(msg.ID)
		if seen[id] {
			t.Fatalf("duplicate request id %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %d ids, want 3", len(seen))
	}
}

func frame(body string) string {
	return "Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func decodeAll(t *testing.T, stream string) []message {
	t.Helper()
	var out []message
	c := newConn(strings.NewReader(stream), io.Discard)
	for {
		msg, err := c.read()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		out = append(out, *msg)
	}
}

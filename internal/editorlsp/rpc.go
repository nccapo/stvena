// Package editorlsp serves Stvena's editor surface over the Language Server
// Protocol. Zed extensions are sandboxed WebAssembly with no editor, UI, or
// command API, so everything the VS Code extension draws directly is drawn here
// through standard LSP instead: follow navigation, accept/reject code lenses,
// code actions, inlay hints, diagnostics, and a progress status item.
//
// The server is editor-agnostic. Zed launches it through a thin extension
// (github.com/nccapo/zed-stvena); any LSP-capable editor can launch the same
// binary directly. See docs/zed-integration.md and docs/editor-integration.md.
package editorlsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// JSON-RPC error codes this server uses.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	// LSP reserves this one for requests that arrive after shutdown.
	codeInvalidRequestAfterShutdown = -32600
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// message is one JSON-RPC 2.0 frame in either direction. Requests, responses
// and notifications share a struct so the read loop can stay in one place; the
// combination of fields present decides what a frame is.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (m *message) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }
func (m *message) isRequest() bool  { return m.Method != "" && len(m.ID) > 0 }

// conn frames JSON-RPC over a stream. Stdout is the protocol channel: nothing
// but frames may ever reach it, so every diagnostic in this package goes to the
// log writer (stderr) or to window/logMessage.
type conn struct {
	reader *bufio.Reader

	mu     sync.Mutex
	writer io.Writer
	nextID int
	closed bool
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{reader: bufio.NewReaderSize(r, 64*1024), writer: w, nextID: 1}
}

// maxFrame bounds an incoming frame. Editors send buffer text on didOpen and
// didChange, so this is generous, but it is not unbounded.
const maxFrame = 32 * 1024 * 1024

// read returns the next frame, or io.EOF once the editor closes the stream.
func (c *conn) read() (*message, error) {
	length := -1
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("malformed header %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "content-length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("malformed Content-Length: %w", err)
			}
		}
	}
	if length < 0 {
		return nil, errors.New("frame without Content-Length")
	}
	if length > maxFrame {
		return nil, fmt.Errorf("frame of %d bytes exceeds the limit", length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, err
	}
	var msg message
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("%w: %v", errParse, err)
	}
	return &msg, nil
}

var errParse = errors.New("invalid JSON frame")

func (c *conn) send(msg message) error {
	msg.JSONRPC = "2.0"
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("connection closed")
	}
	if _, err := fmt.Fprintf(c.writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.writer.Write(body)
	return err
}

func (c *conn) notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.send(message{Method: method, Params: raw})
}

// request sends a server-to-client request. The reply is matched by id and
// dropped: every request this server makes (showDocument, the refreshes,
// workDoneProgress/create) is advisory, and blocking the event loop on an
// editor round trip would stall the descriptor poll.
func (c *conn) request(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()
	return c.send(message{ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw})
}

func (c *conn) reply(id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.send(message{ID: id, Result: raw})
}

func (c *conn) replyError(id json.RawMessage, code int, format string, args ...any) error {
	return c.send(message{ID: id, Error: &rpcError{Code: code, Message: fmt.Sprintf(format, args...)}})
}

func (c *conn) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}

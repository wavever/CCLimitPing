package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Codex's app-server is the JSON-RPC protocol the Codex IDE extension and
// desktop app drive the agent through (`codex app-server`, JSON lines over
// stdio). Unlike the private HTTP endpoints limitping otherwise reads, its
// methods come with a published schema (`codex app-server
// generate-json-schema`), so an irreversible call — spending a reset credit —
// goes through here instead of through a request body guessed from a binary.
//
// Starting it costs a process and a couple of seconds, which is fine for a
// redemption but not for a once-a-minute usage poll; that keeps using HTTP.

// JSON-RPC error codes that mean a Codex too old to know the method.
const (
	codexRPCInvalidRequest = -32600
	codexRPCMethodNotFound = -32601
)

// codexAppServerCall runs one app-server request; tests swap it for a fake.
var codexAppServerCall = runCodexAppServer

// errCodexAppServerUnavailable marks a failure that happened before the request
// itself was sent — no codex binary, a version without the app-server or the
// method — so falling back to another route cannot repeat an attempt that
// reached the backend.
var errCodexAppServerUnavailable = errors.New("codex app-server unavailable")

// codexRPCError is a JSON-RPC error answer to the request itself.
type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

type codexRPCMessage struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *codexRPCError  `json:"error"`
}

// runCodexAppServer starts `codex app-server`, performs the initialize
// handshake, sends method with params and returns the result. Notifications the
// server pushes meanwhile are skipped.
func runCodexAppServer(ctx context.Context, method string, params any) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		_ = cmd.Wait()
	}()

	return codexRPCExchange(stdin, stdout, method, params)
}

// codexRPCExchange speaks the protocol over an already-running server's pipes.
func codexRPCExchange(w io.Writer, r io.Reader, method string, params any) (json.RawMessage, error) {
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 64<<10), 16<<20)
	send := func(msg map[string]any) error {
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	await := func(id int) (codexRPCMessage, error) {
		for lines.Scan() {
			var msg codexRPCMessage
			if json.Unmarshal(lines.Bytes(), &msg) != nil || msg.ID == nil || *msg.ID != id {
				continue
			}
			return msg, nil
		}
		if err := lines.Err(); err != nil {
			return codexRPCMessage{}, err
		}
		return codexRPCMessage{}, io.ErrUnexpectedEOF
	}

	if err := send(map[string]any{
		"id":     0,
		"method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "limitping", "version": "1"}},
	}); err != nil {
		return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, err)
	}
	init, err := await(0)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize: %v", errCodexAppServerUnavailable, err)
	}
	if init.Error != nil {
		return nil, fmt.Errorf("%w: initialize: %v", errCodexAppServerUnavailable, init.Error)
	}
	if err := send(map[string]any{"method": "initialized"}); err != nil {
		return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, err)
	}

	if err := send(map[string]any{"id": 1, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, err)
	}
	resp, err := await(1)
	if err != nil {
		return nil, fmt.Errorf("codex app-server %s: %w", method, err)
	}
	if resp.Error != nil {
		if resp.Error.unknownMethod() {
			return nil, fmt.Errorf("%w: %v", errCodexAppServerUnavailable, resp.Error)
		}
		return nil, resp.Error
	}
	return resp.Result, nil
}

// unknownMethod reports a request the server rejected before acting on it
// because it does not know the method. Codex 0.160 answers that as an invalid
// request ("unknown variant `…`") rather than JSON-RPC's method-not-found.
func (e *codexRPCError) unknownMethod() bool {
	return e.Code == codexRPCMethodNotFound ||
		(e.Code == codexRPCInvalidRequest && strings.Contains(e.Message, "unknown variant"))
}

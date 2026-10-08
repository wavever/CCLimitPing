package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestCodexRedeemGoesThroughAppServer(t *testing.T) {
	fakeCodexHome(t)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("the HTTP endpoint was called although the app-server answered: %s", req.URL)
		return nil, nil
	})
	var gotMethod string
	var gotParams map[string]string
	fakeCodexAppServer(t, func(method string, params any) (json.RawMessage, error) {
		gotMethod = method
		gotParams, _ = params.(map[string]string)
		return json.RawMessage(`{"outcome":"nothingToReset"}`), nil
	})

	got, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(),
		usage.ResetCredit{ID: "RateLimitResetCredit_abc"})
	if err != nil || got.Outcome != RedeemNothingToReset {
		t.Fatalf("outcome = %q (err %v), want %q", got, err, RedeemNothingToReset)
	}
	if gotMethod != "account/rateLimitResetCredit/consume" {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotParams["creditId"] != "RateLimitResetCredit_abc" || gotParams["idempotencyKey"] == "" {
		t.Fatalf("params = %v, want the credit id and an idempotency key", gotParams)
	}
}

// A JSON-RPC error that is not "method not found" may come from the backend, so
// it must not trigger a second attempt over HTTP.
func TestCodexRedeemDoesNotFallBackAfterABackendError(t *testing.T) {
	fakeCodexHome(t)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("fell back to HTTP after the app-server answered with an error")
		return nil, nil
	})
	fakeCodexAppServer(t, func(string, any) (json.RawMessage, error) {
		return nil, &codexRPCError{Code: -32000, Message: "upstream failure"}
	})
	if _, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), usage.ResetCredit{}); err == nil {
		t.Fatal("expected the app-server error")
	}
}

func TestCodexAutoRedeemTargetsTheExpiringCreditByID(t *testing.T) {
	fakeCodexHome(t)
	var params map[string]string
	fakeCodexAppServer(t, func(_ string, p any) (json.RawMessage, error) {
		params, _ = p.(map[string]string)
		return json.RawMessage(`{"outcome":"reset"}`), nil
	})
	now := time.Now()
	u := &usage.Usage{ResetCredits: &usage.ResetCredits{AvailableCount: 2, Credits: []usage.ResetCredit{
		{ID: "later", Status: "available", ExpiresAt: now.Add(20 * 24 * time.Hour)},
		{ID: "soon", Status: "available", ExpiresAt: now.Add(30 * time.Minute)},
	}}}
	got, err := NewCodex(config.ProviderConfig{}).AutoRedeemResetCredit(context.Background(), u)
	if err != nil || got.Outcome != RedeemReset {
		t.Fatalf("outcome = %q (err %v)", got, err)
	}
	if params["creditId"] != "soon" {
		t.Fatalf("creditId = %q, want the credit about to lapse", params["creditId"])
	}
	if params["idempotencyKey"] != creditIdempotencyKey(usage.ResetCredit{ID: "soon"}) {
		t.Fatalf("idempotencyKey is not derived from the credit, so a lost response could spend twice")
	}
}

// fakeServer answers the handshake and then the request, interleaving the
// notifications a real app-server pushes unprompted.
func fakeServer(t *testing.T, answer string) (io.Writer, io.Reader, <-chan map[string]any) {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	got := make(chan map[string]any, 1)
	go func() {
		defer respW.Close()
		in := bufio.NewScanner(reqR)
		for in.Scan() {
			var msg map[string]any
			_ = json.Unmarshal(in.Bytes(), &msg)
			switch msg["method"] {
			case "initialize":
				io.WriteString(respW, `{"method":"account/updated","params":{}}`+"\n")
				io.WriteString(respW, `{"id":0,"result":{"userAgent":"x"}}`+"\n")
			case "initialized":
			default:
				got <- msg
				io.WriteString(respW, `{"method":"remoteControl/status/changed","params":{}}`+"\n")
				io.WriteString(respW, answer+"\n")
				return
			}
		}
	}()
	t.Cleanup(func() { reqR.Close(); respR.Close() })
	return reqW, respR, got
}

func TestCodexRPCExchangeSkipsNotifications(t *testing.T) {
	w, r, got := fakeServer(t, `{"id":1,"result":{"outcome":"reset"}}`)
	result, err := codexRPCExchange(w, r, "account/rateLimitResetCredit/consume", map[string]string{"idempotencyKey": "k"})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if string(result) != `{"outcome":"reset"}` {
		t.Fatalf("result = %s", result)
	}
	req := <-got
	if req["method"] != "account/rateLimitResetCredit/consume" {
		t.Fatalf("request = %v", req)
	}
}

func TestCodexRPCExchangeMethodNotFoundIsUnavailable(t *testing.T) {
	w, r, _ := fakeServer(t, `{"id":1,"error":{"code":-32601,"message":"unknown method"}}`)
	_, err := codexRPCExchange(w, r, "account/rateLimitResetCredit/consume", nil)
	if !errors.Is(err, errCodexAppServerUnavailable) {
		t.Fatalf("err = %v, want errCodexAppServerUnavailable so the caller can fall back", err)
	}
}

func TestCodexRPCExchangeUnknownVariantIsUnavailable(t *testing.T) {
	w, r, _ := fakeServer(t, `{"id":1,"error":{"code":-32600,"message":"Invalid request: unknown variant `+"`"+`account/x`+"`"+`, expected one of …"}}`)
	_, err := codexRPCExchange(w, r, "account/x", nil)
	if !errors.Is(err, errCodexAppServerUnavailable) {
		t.Fatalf("err = %v, want errCodexAppServerUnavailable", err)
	}
}

func TestCodexRPCExchangeServerGoneBeforeAnswer(t *testing.T) {
	_, err := codexRPCExchange(io.Discard, strings.NewReader(""), "x", nil)
	if !errors.Is(err, errCodexAppServerUnavailable) {
		t.Fatalf("err = %v: a server that dies during the handshake never saw the request", err)
	}
}

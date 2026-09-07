package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/codexstate"
	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func fakeCodexCLI(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native Windows PTY is unsupported")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func quotaResponse(reset int64) string {
	return fmt.Sprintf(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":%d}}}`, reset)
}

func TestVerifiedPingReadsBeforeAndAfterWithoutWaitingMinute(t *testing.T) {
	fakeCodexHome(t)
	fakeCodexCLI(t, `printf '\033]9;done\007'`)
	old := usageHTTPClient
	defer func() { usageHTTPClient = old }()
	reads := 0
	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/usage") {
			t.Fatalf("unexpected detail read %s", req.URL.Path)
		}
		reads++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(quotaResponse(time.Now().Add(7 * 24 * time.Hour).Unix()))), Header: make(http.Header)}, nil
	})}
	start := time.Now()
	res, err := NewCodex(config.ProviderConfig{Enabled: true}).Trigger(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatalf("reads=%d", reads)
	}
	if !res.TurnCompleted {
		t.Fatal("missing completion evidence")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("manual ping blocked")
	}
	if res.Verification == nil || res.Verification.Weekly.State != codexstate.Unknown || res.Verification.Weekly.DueAt.IsZero() {
		t.Fatalf("%+v", res.Verification)
	}
}

func TestFailedTriggerStillReadsQuotaAfterwards(t *testing.T) {
	fakeCodexHome(t)
	fakeCodexCLI(t, "exit 1")
	old := usageHTTPClient
	defer func() { usageHTTPClient = old }()
	reads := 0
	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		reads++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(quotaResponse(time.Now().Add(7 * 24 * time.Hour).Unix()))), Header: make(http.Header)}, nil
	})}
	res, err := NewCodex(config.ProviderConfig{}).Trigger(context.Background(), false)
	if err == nil || reads != 2 || res.Verification == nil {
		t.Fatal(err, reads, res)
	}
}

func TestAccountSwitchReloadsIdentity(t *testing.T) {
	fakeCodexHome(t)
	old := usageHTTPClient
	defer func() { usageHTTPClient = old }()
	expected := "account-123"
	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("ChatGPT-Account-Id") != expected {
			t.Fatalf("stale account header: %s", req.Header.Get("ChatGPT-Account-Id"))
		}
		body := quotaResponse(time.Now().Add(7 * 24 * time.Hour).Unix())
		if strings.HasSuffix(req.URL.Path, "reset-credits") {
			body = `{"available_count":0}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	c := NewCodex(config.ProviderConfig{})
	if _, err := c.ReadUsage(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected = "account-new"
	if err := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), []byte(`{"tokens":{"access_token":"different-valid-token","account_id":"account-new"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	u, err := c.ReadUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Verification.Weekly.State != codexstate.Unknown {
		t.Fatal(u.Verification)
	}
}

func TestDryRunNeverReadsOrWritesState(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	old := usageHTTPClient
	defer func() { usageHTTPClient = old }()
	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("dry run read usage"); return nil, nil })}
	if _, err := NewCodex(config.ProviderConfig{}).Trigger(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}

func TestQuotaPrecheckFailureNeverSends(t *testing.T) {
	for _, status := range []int{403, 503} {
		for _, automatic := range []bool{false, true} {
			t.Run(fmt.Sprint(status, automatic), func(t *testing.T) {
				fakeCodexHome(t)
				marker := filepath.Join(t.TempDir(), "sent")
				t.Setenv("TEST_SENT", marker)
				fakeCodexCLI(t, `touch "$TEST_SENT"`)
				reads := 0
				useTransport(t, func(*http.Request) (*http.Response, error) {
					reads++
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{"Retry-After": []string{"120"}}}, nil
				})
				var reserve PingReservation
				if automatic {
					reserve = func(codexstate.Store, string, string, *usage.Usage) (string, error) {
						t.Fatal("failed precheck must not reserve a ping")
						return "", nil
					}
				}
				start := time.Now()
				res, err := pingVerified(context.Background(), "codex", config.ProviderConfig{}, false, reserve)
				var httpErr *UsageHTTPError
				if res != nil || !errors.As(err, &httpErr) || httpErr.StatusCode != status || !strings.Contains(err.Error(), "ping not sent: quota precheck failed") {
					t.Fatal(res, err)
				}
				if reads != 1 || time.Since(start) > 5*time.Second {
					t.Fatal("precheck retried or waited", reads)
				}
				if !httpErr.RetryAfter.After(start.Add(time.Minute)) {
					t.Fatal("Retry-After was lost")
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("CLI was executed", err)
				}
			})
		}
	}
}

func TestQuotaNetworkFailureDoesNotRetry(t *testing.T) {
	fakeCodexHome(t)
	reads := 0
	useTransport(t, func(*http.Request) (*http.Response, error) {
		reads++
		return nil, io.ErrUnexpectedEOF
	})
	res, err := NewCodex(config.ProviderConfig{}).Trigger(context.Background(), false)
	if res != nil || !errors.Is(err, io.ErrUnexpectedEOF) || reads != 1 {
		t.Fatal(res, err, reads)
	}
}

func TestPostcheckFailureReportsSentWithoutRetry(t *testing.T) {
	fakeCodexHome(t)
	fakeCodexCLI(t, `printf '\033]9;done\007'`)
	reads := 0
	useTransport(t, func(*http.Request) (*http.Response, error) {
		reads++
		status, body := 200, quotaResponse(time.Now().Add(7*24*time.Hour).Unix())
		if reads > 1 {
			status, body = 503, "{}"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Retry-After": []string{"1200"}}}, nil
	})
	res, err := NewCodex(config.ProviderConfig{}).Trigger(context.Background(), false)
	if err != nil || reads != 2 || res == nil || !strings.Contains(res.Verification.Warning, "post-ping quota read failed") {
		t.Fatal(res, err, reads)
	}
	var postErr *UsageHTTPError
	if res.PreVerification == nil || res.PreVerification == res.Verification {
		t.Fatal("pre-ping verification was not retained separately")
	}
	if !errors.As(res.PostcheckErr, &postErr) || postErr.StatusCode != 503 || !postErr.RetryAfter.After(time.Now().Add(19*time.Minute)) {
		t.Fatal("postcheck retry metadata lost", res.PostcheckErr)
	}
}

func TestMissingCredentialsIsAuthenticationFailure(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	res, err := NewCodex(config.ProviderConfig{}).Trigger(context.Background(), false)
	var authErr *AuthenticationError
	if res != nil || !errors.As(err, &authErr) || !strings.Contains(err.Error(), "ping not sent") {
		t.Fatal(res, err)
	}
}

func TestPrecheckReloadsCredentialsOn401(t *testing.T) {
	fakeCodexHome(t)
	fakeCodexCLI(t, `printf '\033]9;done\007'`)
	reads := 0
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		reads++
		if reads == 1 {
			if err := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), []byte(`{"tokens":{"access_token":"new-token","account_id":"account-123"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
		}
		if req.Header.Get("Authorization") != "Bearer new-token" {
			t.Fatal("stale token")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(quotaResponse(time.Now().Add(7 * 24 * time.Hour).Unix()))), Header: make(http.Header)}, nil
	})
	if _, err := NewCodex(config.ProviderConfig{}).Trigger(context.Background(), false); err != nil || reads != 3 {
		t.Fatal(err, reads)
	}
}

func TestAutoRedeemRejectsChangedObservationAccount(t *testing.T) {
	fakeCodexHome(t)
	requests := 0
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		requests++
		t.Fatal("must not spend another account's credit")
		return nil, nil
	})
	u := &usage.Usage{QuotaAccount: "previous-account", ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
		{Status: "available", ExpiresAt: time.Now().Add(30 * time.Minute)},
	}}}
	_, err := NewCodex(config.ProviderConfig{}).AutoRedeemResetCredit(context.Background(), u)
	if err == nil || requests != 0 {
		t.Fatal(err, requests)
	}
}

func TestAutoRedeemRechecksIdentityOnAuthenticationRetry(t *testing.T) {
	fakeCodexHome(t)
	requests := 0
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if requests > 1 {
			t.Fatal("retried redemption against changed account")
		}
		if err := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), []byte(`{"tokens":{"access_token":"new-token","account_id":"new-account"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	u := &usage.Usage{QuotaAccount: "account-123", ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
		{Status: "available", ExpiresAt: time.Now().Add(30 * time.Minute)},
	}}}
	_, err := NewCodex(config.ProviderConfig{}).AutoRedeemResetCredit(context.Background(), u)
	if err == nil || requests != 1 {
		t.Fatal(err, requests)
	}
}

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/usage"
)

const testClaudeOrg = "11111111-2222-3333-4444-555555555555"

// fakeClaudeConfigDir points CLAUDE_CONFIG_DIR at a temp dir, so a test never
// reads the real ~/.claude.json, and limitping's own config dir at another, so
// the claims it records stay in the test. An empty org writes no config at all.
func fakeClaudeConfigDir(t *testing.T, org string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if org == "" {
		return
	}
	cfg := `{"oauthAccount":{"organizationUuid":"` + org + `"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testClaude() *Claude {
	return &Claude{auth: staticTokenSource{token: "oauth-token"}}
}

func jsonResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// claudeUsageWithCards is the usage response as the live endpoint returned it
// on 2026-09-30, plus a second, queued grant and a spent one.
const claudeUsageWithCards = `{
	"five_hour": {"utilization": 5.0, "resets_at": "2026-09-30T20:49:59.800152+00:00"},
	"seven_day": {"utilization": 1.0, "resets_at": "2026-10-03T19:59:59.800176+00:00"},
	"cedar_ember": {
		"eligible": true,
		"ineligible_reason": null,
		"at_limit": false,
		"exhausted": [],
		"grants": [
			{
				"id": "opus55-launch-promax-20260921",
				"label": "Claude Opus 5.5 launch: one usage-limit reset for Pro and Max",
				"resets_total": 1,
				"resets_left": 1,
				"starts_at": "2026-09-22T16:00:00+00:00",
				"ends_at": "2099-10-22T16:00:00+00:00",
				"clears": ["five_hour", "seven_day", "seven_day_overage_included"],
				"paused": false,
				"usable_now": true,
				"use_requires_limit": false,
				"percent_used": {"five_hour": 5, "seven_day": 1},
				"blocking": [],
				"arm": null
			},
			{
				"id": "loyalty-2026",
				"label": "Thanks for sticking around",
				"resets_total": 3,
				"resets_left": 2,
				"starts_at": null,
				"ends_at": null,
				"clears": ["five_hour"],
				"paused": false,
				"usable_now": true
			},
			{
				"id": "spent-grant",
				"label": "Spent",
				"resets_total": 1,
				"resets_left": 0,
				"clears": ["seven_day"],
				"usable_now": false
			}
		],
		"next_grant_id": "opus55-launch-promax-20260921",
		"weekly_resets_at": "2026-10-03T20:00:00+00:00",
		"cooldown_until": null,
		"event_props": null
	},
	"juniper_tide": null
}`

func TestClaudeReadResetCreditsReportsTheCards(t *testing.T) {
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != claudeResetCardsURL {
			t.Fatalf("card read URL = %s, want %s", req.URL, claudeResetCardsURL)
		}
		// The server only offers reset cards to the Claude Code CLI.
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "claude-cli/") || !strings.HasSuffix(got, "(external, cli)") {
			t.Fatalf("User-Agent = %q, want Claude Code's CLI agent", got)
		}
		return jsonResponse(req, http.StatusOK, claudeUsageWithCards), nil
	})

	u, err := testClaude().ReadUsageWithResetCredits(context.Background())
	if err != nil {
		t.Fatalf("ReadUsageWithResetCredits: %v", err)
	}
	// One request carries both halves.
	if u.FiveHour.UsedPercent != 5 || u.Weekly.UsedPercent != 1 {
		t.Fatalf("windows = %v / %v, want them read from the same response", u.FiveHour.UsedPercent, u.Weekly.UsedPercent)
	}
	rc := u.ResetCredits
	if rc == nil {
		t.Fatal("ResetCredits = nil, want the grants")
	}
	if rc.AvailableCount != 3 {
		t.Fatalf("AvailableCount = %d, want 3 (1 + 2 + 0)", rc.AvailableCount)
	}
	if len(rc.Credits) != 3 {
		t.Fatalf("credits = %d, want one per grant", len(rc.Credits))
	}

	launch := rc.Credits[0]
	if launch.Status != "available" || launch.ID != "opus55-launch-promax-20260921" || launch.Left != 1 || launch.Total != 1 {
		t.Fatalf("launch card = %+v", launch)
	}
	if launch.RequiresLimit {
		t.Fatal("launch card RequiresLimit = true, want the explicit false honored")
	}
	if want := []string{usage.ClearsFiveHour, usage.ClearsWeekly}; !reflect.DeepEqual(launch.Clears, want) {
		t.Fatalf("launch card clears = %v, want %v (the overage bucket folded into weekly)", launch.Clears, want)
	}
	if want := time.Date(2099, 10, 22, 16, 0, 0, 0, time.UTC); !launch.ExpiresAt.Equal(want) {
		t.Fatalf("launch card expires = %v, want %v", launch.ExpiresAt, want)
	}

	// Usable, but the server only accepts next_grant_id.
	loyalty := rc.Credits[1]
	if loyalty.Status != ClaudeGrantQueued || loyalty.Redeemable(time.Now()) {
		t.Fatalf("loyalty card = %+v, want queued and not redeemable", loyalty)
	}
	if !loyalty.ExpiresAt.IsZero() || !loyalty.RequiresLimit {
		t.Fatalf("loyalty card = %+v, want no expiry and the at-limit default", loyalty)
	}
	if rc.Credits[2].Status != "redeemed" {
		t.Fatalf("spent card status = %q, want redeemed", rc.Credits[2].Status)
	}

	// The server's own readings come along.
	if want := map[string]float64{usage.ClearsFiveHour: 5, usage.ClearsWeekly: 1}; !reflect.DeepEqual(launch.PercentUsed, want) {
		t.Fatalf("launch card percent used = %v, want %v", launch.PercentUsed, want)
	}
	if !rc.LimitState || rc.AtLimit || len(rc.Exhausted) != 0 || !rc.CooldownUntil.IsZero() {
		t.Fatalf("limit state = %t/%t/%v/%v, want the server's 'not at a limit'", rc.LimitState, rc.AtLimit, rc.Exhausted, rc.CooldownUntil)
	}
}

func TestClaudeResetCreditsTakeTheServersReadings(t *testing.T) {
	var s claudeResetStatus
	raw := `{
		"eligible": true,
		"at_limit": true,
		"exhausted": ["seven_day_overage_included", "seven_day_opus"],
		"cooldown_until": "2026-10-08T12:00:00Z",
		"next_grant_id": "g",
		"grants": [{
			"id": "g", "resets_left": 1, "usable_now": true,
			"clears": ["five_hour", "seven_day", "seven_day_overage_included"],
			"percent_used": {"five_hour": 40, "seven_day": 70, "seven_day_overage_included": 90,
				"seven_day_opus": 100, "half": 5.5, "over": 150, "neg": -1, "text": "80"},
			"blocking": ["seven_day_overage_included"]
		}]
	}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	rc := claudeResetCreditsToUsage(&s, time.Now())
	if !rc.LimitState || !rc.AtLimit {
		t.Fatalf("limit state = %t/%t, want the server's at-limit verdict", rc.LimitState, rc.AtLimit)
	}
	// Every place the server names a window folds the same way.
	if want := []string{usage.ClearsWeekly, "seven_day_opus"}; !reflect.DeepEqual(rc.Exhausted, want) {
		t.Fatalf("exhausted = %v, want %v", rc.Exhausted, want)
	}
	if want := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC); !rc.CooldownUntil.Equal(want) {
		t.Fatalf("cooldown until = %v, want %v", rc.CooldownUntil, want)
	}
	g := rc.Credits[0]
	// Whole percentages only, for the windows the grant clears, the folded
	// overage bucket standing in for weekly at the higher reading.
	if want := map[string]float64{usage.ClearsFiveHour: 40, usage.ClearsWeekly: 90}; !reflect.DeepEqual(g.PercentUsed, want) {
		t.Fatalf("percent used = %v, want %v", g.PercentUsed, want)
	}
	if want := []string{usage.ClearsWeekly}; !reflect.DeepEqual(g.Blocking, want) {
		t.Fatalf("blocking = %v, want %v", g.Blocking, want)
	}
}

func TestClaudeResetCreditsWithoutLimitStateLeaveItUnknown(t *testing.T) {
	s := claudeResetStatus{Eligible: true, NextGrantID: "g", Grants: []claudeResetGrant{{ID: "g", ResetsLeft: 1, UsableNow: true}}}
	if rc := claudeResetCreditsToUsage(&s, time.Now()); rc.LimitState {
		t.Fatal("a status block without at_limit or exhausted claimed to know the limit state")
	}
}

func TestClaudeResetCreditsReportWhyTheyAreWithheld(t *testing.T) {
	for reason, want := range map[string]string{
		"cli_version": "cli_version",
		"surface":     "surface",
		"tier":        "tier",
		// These just mean nothing is on offer; "no reset cards" says that.
		"no_grant":   "",
		"config_off": "",
	} {
		r := reason
		rc := claudeResetCreditsToUsage(&claudeResetStatus{Eligible: false, IneligibleReason: &r}, time.Now())
		got := ""
		if rc != nil {
			got = rc.UnavailableReason
			if len(rc.Credits) != 0 {
				t.Fatalf("%s: an ineligible account reported cards", reason)
			}
		}
		if got != want {
			t.Fatalf("%s: unavailable reason = %q, want %q", reason, got, want)
		}
	}
}

// claudeCardsTransport serves the plain usage read and the card read apart,
// counting each, so a test can see which requests a ReadUsage made.
func claudeCardsTransport(t *testing.T, cardsStatus int) (usageReads, cardReads *int) {
	t.Helper()
	usageReads, cardReads = new(int), new(int)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case claudeUsageURL:
			*usageReads++
			// The poll keeps presenting as it always has.
			if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "claude-code/") {
				t.Fatalf("usage poll User-Agent = %q, want the unchanged claude-code agent", got)
			}
			return jsonResponse(req, http.StatusOK, `{"five_hour":{"utilization":5},"seven_day":{"utilization":1}}`), nil
		case claudeResetCardsURL:
			*cardReads++
			return jsonResponse(req, cardsStatus, claudeUsageWithCards), nil
		case claudeCountTokensURL:
			// The subscription probe a 429 triggers; inconclusive here.
			return jsonResponse(req, http.StatusOK, `{"input_tokens":1}`), nil
		}
		t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		return nil, nil
	})
	return usageReads, cardReads
}

// ReadUsage is what one-shot commands (ping, bg status, continue's first look)
// read through; with auto_redeem on or off, it must never present as the CLI.
func TestClaudeReadUsageNeverReadsResetCards(t *testing.T) {
	for _, autoRedeem := range []bool{false, true} {
		usageReads, cardReads := claudeCardsTransport(t, http.StatusOK)
		c := testClaude()
		c.cfg.AutoRedeem = autoRedeem
		for i := 0; i < 2; i++ {
			u, err := c.ReadUsage(context.Background())
			if err != nil {
				t.Fatalf("ReadUsage: %v", err)
			}
			if u.ResetCredits != nil {
				t.Fatalf("auto_redeem=%t: cards = %+v, want none from a plain read", autoRedeem, u.ResetCredits)
			}
		}
		if *usageReads != 2 || *cardReads != 0 {
			t.Fatalf("auto_redeem=%t: usage reads %d, card reads %d; want plain reads only", autoRedeem, *usageReads, *cardReads)
		}
	}
}

func TestClaudeAutoRedeemReadReadsResetCardsHourly(t *testing.T) {
	usageReads, cardReads := claudeCardsTransport(t, http.StatusOK)
	c := testClaude()

	for i := 0; i < 3; i++ {
		u, err := c.ReadUsageForAutoRedeem(context.Background())
		if err != nil {
			t.Fatalf("ReadUsageForAutoRedeem: %v", err)
		}
		if u.ResetCredits == nil || u.ResetCredits.AvailableCount != 3 {
			t.Fatalf("poll %d: cards = %+v, want the cached cards on every poll", i, u.ResetCredits)
		}
		// Only the read that fetched them carries the server's readings; an
		// hour-old verdict must not overrule the windows read beside it.
		if fresh := i == 0; u.ResetCredits.LimitState != fresh || (u.ResetCredits.Credits[0].PercentUsed != nil) != fresh {
			t.Fatalf("poll %d: cards = %+v, want readings only from the card read itself", i, u.ResetCredits)
		}
	}
	// The hourly card read stands in for that poll's usage read; it is never
	// an extra request.
	if *cardReads != 1 || *usageReads != 2 {
		t.Fatalf("usage reads %d, card reads %d; want 1 combined read then plain polls", *usageReads, *cardReads)
	}

	// Once the hour is up, the cards are read again.
	c.cardsReadAt = time.Now().Add(-claudeCardsPollPeriod - time.Minute)
	if _, err := c.ReadUsageForAutoRedeem(context.Background()); err != nil {
		t.Fatalf("ReadUsageForAutoRedeem: %v", err)
	}
	if *cardReads != 2 {
		t.Fatalf("card reads = %d after the period, want 2", *cardReads)
	}
}

func TestClaudeAutoRedeemReadSurvivesAFailedCardRead(t *testing.T) {
	usageReads, cardReads := claudeCardsTransport(t, http.StatusBadRequest)
	c := testClaude()

	for i := 0; i < 2; i++ {
		u, err := c.ReadUsageForAutoRedeem(context.Background())
		if err != nil {
			t.Fatalf("ReadUsageForAutoRedeem: %v, want usage despite the card read failing", err)
		}
		if u.FiveHour.UsedPercent != 5 || u.ResetCredits != nil {
			t.Fatalf("usage = %+v, want the windows and no cards", u)
		}
		// The fallback says the cards are unknown, not absent.
		if fallback := i == 0; (u.ResetCreditsError != nil) != fallback {
			t.Fatalf("poll %d: ResetCreditsError = %v, want it on the fallback read only", i, u.ResetCreditsError)
		}
	}
	if *cardReads != 1 || *usageReads != 2 {
		t.Fatalf("usage reads %d, card reads %d; want one fallback, then a failure throttled like a success", *usageReads, *cardReads)
	}
}

// Only loops that auto-redeem read through ReadUsageForAutoRedeem.
func TestReadUsageForLoopPiggybacksOnlyWhenAutoRedeeming(t *testing.T) {
	usageReads, cardReads := claudeCardsTransport(t, http.StatusOK)
	if _, err := ReadUsageForLoop(context.Background(), testClaude(), false); err != nil {
		t.Fatal(err)
	}
	if *usageReads != 1 || *cardReads != 0 {
		t.Fatalf("usage reads %d, card reads %d; a loop that does not redeem must read plainly", *usageReads, *cardReads)
	}
	if _, err := ReadUsageForLoop(context.Background(), testClaude(), true); err != nil {
		t.Fatal(err)
	}
	if *cardReads != 1 {
		t.Fatalf("card reads = %d, want the auto-redeem loop's first read to carry the cards", *cardReads)
	}
}

func TestClaudeCardReadDoesNotRetryARateLimit(t *testing.T) {
	usageReads, cardReads := claudeCardsTransport(t, http.StatusTooManyRequests)
	if _, err := testClaude().ReadUsageWithResetCredits(context.Background()); err == nil {
		t.Fatal("a rate-limited read reported success")
	}
	// Falling back would just be a second request into the same limit.
	if *cardReads != 1 || *usageReads != 0 {
		t.Fatalf("usage reads %d, card reads %d; want the 429 returned without a fallback", *usageReads, *cardReads)
	}
}

// A server error is not about the card request: the plain read would hit the
// same failing endpoint, so falling back only doubles the traffic.
func TestClaudeCardReadDoesNotRetryAServerError(t *testing.T) {
	usageReads, cardReads := claudeCardsTransport(t, http.StatusBadGateway)
	if _, err := testClaude().ReadUsageWithResetCredits(context.Background()); err == nil {
		t.Fatal("a failed read reported success")
	}
	if *usageReads != 0 {
		t.Fatalf("usage reads %d, card reads %d; want the 5xx returned without a fallback", *usageReads, *cardReads)
	}
}

func TestClaudeClaimForgetsTheCachedCards(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	c := testClaude()
	c.cards = &usage.ResetCredits{AvailableCount: 1}
	c.cardsReadAt = time.Now()
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(req, http.StatusOK, `{"result":"reset"}`), nil
	})
	if _, err := c.RedeemResetCredit(context.Background(), usage.ResetCredit{ID: "g"}); err != nil {
		t.Fatalf("RedeemResetCredit: %v", err)
	}
	if c.cards != nil || !c.cardsReadAt.IsZero() {
		t.Fatal("a claim left the pre-claim cards cached")
	}
}

func TestClaudeResetCreditsToUsageSkipsIneligibleAccounts(t *testing.T) {
	now := time.Now()
	if got := claudeResetCreditsToUsage(nil, now); got != nil {
		t.Fatalf("no block = %+v, want nil", got)
	}
	ineligible := &claudeResetStatus{Eligible: false, Grants: []claudeResetGrant{{ID: "x", ResetsLeft: 1, UsableNow: true}}}
	if got := claudeResetCreditsToUsage(ineligible, now); got != nil {
		t.Fatalf("ineligible = %+v, want nil", got)
	}
	if got := claudeResetCreditsToUsage(&claudeResetStatus{Eligible: true}, now); got != nil {
		t.Fatalf("no grants = %+v, want nil", got)
	}
}

func TestClaudeGrantStatus(t *testing.T) {
	base := claudeResetGrant{ID: "g", ResetsLeft: 1, UsableNow: true}
	cases := []struct {
		name    string
		mutate  func(*claudeResetGrant)
		next    string
		expired bool
		want    string
	}{
		{"next and usable", func(*claudeResetGrant) {}, "g", false, "available"},
		{"not next", func(*claudeResetGrant) {}, "other", false, ClaudeGrantQueued},
		{"paused", func(g *claudeResetGrant) { g.Paused = true }, "g", false, ClaudeGrantPaused},
		{"not usable yet", func(g *claudeResetGrant) { g.UsableNow = false }, "g", false, ClaudeGrantPending},
		{"used up", func(g *claudeResetGrant) { g.ResetsLeft = 0 }, "g", false, "redeemed"},
		{"expired", func(*claudeResetGrant) {}, "g", true, "expired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := base
			tc.mutate(&g)
			if got := claudeGrantStatus(g, tc.next, tc.expired); got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaudeRedeemResetCreditPostsClaim(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	var sent map[string]string
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		want := "https://api.anthropic.com/api/organizations/" + testClaudeOrg + "/reset_rate_limits"
		if req.Method != http.MethodPost || req.URL.String() != want {
			t.Fatalf("claim = %s %s, want POST %s", req.Method, req.URL, want)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := req.Header.Get("anthropic-beta"); got != claudeOAuthBeta {
			t.Fatalf("anthropic-beta = %q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatalf("claim body is not JSON: %v (%s)", err, body)
		}
		return jsonResponse(req, http.StatusOK, `{"result":"reset","resets_left":0,"cleared":["five_hour","seven_day"]}`), nil
	})

	got, err := testClaude().RedeemResetCredit(context.Background(), usage.ResetCredit{ID: "opus55-launch-promax-20260921"})
	if err != nil || got.Outcome != RedeemReset {
		t.Fatalf("outcome = %q (err %v), want %q", got, err, RedeemReset)
	}
	if sent["program"] != claudeResetProgram || sent["grant_id"] != "opus55-launch-promax-20260921" {
		t.Fatalf("claim body = %v", sent)
	}
	if id := sent["request_id"]; id == "" || len(id) > 64 {
		t.Fatalf("request_id = %q, want a non-empty id of at most 64 chars", id)
	}
}

func TestClaudeRedeemResetCreditMapsOutcomes(t *testing.T) {
	cases := []struct {
		result, reason, want string
	}{
		{"not_limited", "not_limited", RedeemNothingToReset},
		{"already_used", "already_used", RedeemAlreadyRedeemed},
		{"cooldown", "cooldown", RedeemCooldown},
		{"ineligible", "cli_version", RedeemIneligible},
		{"ineligible", "not_next_grant", RedeemIneligible},
	}
	for _, tc := range cases {
		t.Run(tc.result+"/"+tc.reason, func(t *testing.T) {
			fakeClaudeConfigDir(t, testClaudeOrg)
			useTransport(t, func(req *http.Request) (*http.Response, error) {
				return jsonResponse(req, http.StatusOK, `{"result":"`+tc.result+`","reason":"`+tc.reason+`","cooldown_until":"2026-10-08T12:00:00Z"}`), nil
			})
			got, err := testClaude().RedeemResetCredit(context.Background(), usage.ResetCredit{ID: "g"})
			if err != nil || got.Outcome != tc.want {
				t.Fatalf("outcome = %q (err %v), want %q", got, err, tc.want)
			}
			// The reason is the user's clue to what to do next.
			if got.Reason != tc.reason || !got.RetryAt.Equal(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)) {
				t.Fatalf("result = %+v, want the server's reason and cooldown kept", got)
			}
		})
	}
}

// Claude Code reads "unavailable", and any result it does not know, as a claim
// that may still land: never as an outcome a retry may move on from.
func TestClaudeRedeemResetCreditReportsUnsettledClaims(t *testing.T) {
	for _, body := range []string{
		`{"result":"unavailable","reason":"reset_unconfirmed"}`,
		`{"result":"brand_new_code"}`,
		`{"ok":true}`,
		`not json`,
	} {
		fakeClaudeConfigDir(t, testClaudeOrg)
		useTransport(t, func(req *http.Request) (*http.Response, error) {
			return jsonResponse(req, http.StatusOK, body), nil
		})
		got, err := testClaude().RedeemResetCredit(context.Background(), usage.ResetCredit{ID: "g"})
		if err == nil {
			t.Fatalf("body %s: outcome = %q, want an error rather than a claimed result", body, got)
		}
		if !strings.Contains(err.Error(), "Trying again is safe") {
			t.Fatalf("body %s: err = %v, want it to say a retry is safe", body, err)
		}
		if _, ok := (pendingClaims{provider: "claude"}).lookup("g", testClaudeOrg, time.Now()); !ok {
			t.Fatalf("body %s: the claim in doubt was not recorded", body)
		}
	}
}

// claimRecorder answers claims in turn with bodies — a status code prefix
// ("400 ") makes one an HTTP error, and "lost" a connection that drops — and
// records each request id sent.
func claimRecorder(t *testing.T, bodies ...string) *[]string {
	t.Helper()
	ids := new([]string)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		}
		var sent map[string]string
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &sent)
		*ids = append(*ids, sent["request_id"])
		body := bodies[min(len(*ids), len(bodies))-1]
		if body == "lost" {
			return nil, errors.New("connection lost mid-request")
		}
		status := http.StatusOK
		if code, rest, ok := strings.Cut(body, " "); ok && len(code) == 3 {
			fmt.Sscan(code, &status)
			body = rest
		}
		return jsonResponse(req, status, body), nil
	})
	return ids
}

// The finding this guards: a manual redeem after an unconfirmed claim must not
// send a fresh request id, or a multi-reset grant could lose a second reset.
func TestClaudeRedeemRepeatsAClaimInDoubt(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	ids := claimRecorder(t,
		`{"result":"unavailable","reason":"reset_unconfirmed"}`,
		`{"result":"reset"}`,
		`{"result":"reset"}`)
	grant := usage.ResetCredit{ID: "g", Left: 2, Total: 3}

	if _, err := testClaude().RedeemResetCredit(context.Background(), grant); err == nil {
		t.Fatal("an unconfirmed claim reported success")
	}
	// A new process — the user running redeem again.
	if res, err := testClaude().RedeemResetCredit(context.Background(), grant); err != nil || res.Outcome != RedeemReset {
		t.Fatalf("retry = %q (err %v)", res, err)
	}
	if (*ids)[1] != (*ids)[0] {
		t.Fatalf("request ids = %v, want the retry to repeat the claim in doubt", *ids)
	}
	// Settled now: the next claim is a new one.
	if _, err := testClaude().RedeemResetCredit(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	if (*ids)[2] == (*ids)[0] {
		t.Fatalf("request ids = %v, want a fresh id once the claim was settled", *ids)
	}
}

// A lost connection leaves the claim in doubt just the same.
func TestClaudeRedeemRepeatsAClaimLostInTransit(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	ids := claimRecorder(t, "lost", `{"result":"already_used"}`)
	grant := usage.ResetCredit{ID: "g", Left: 1}

	_, err := testClaude().RedeemResetCredit(context.Background(), grant)
	if err == nil || !strings.Contains(err.Error(), "Trying again is safe") {
		t.Fatalf("err = %v, want an unconfirmed claim", err)
	}
	if _, err := testClaude().RedeemResetCredit(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	if len(*ids) != 2 || (*ids)[1] != (*ids)[0] {
		t.Fatalf("request ids = %v, want the retry to repeat the lost request", *ids)
	}
}

// A 5xx is no answer either, and must not read as a usage read failing.
func TestClaudeClaimServerErrorReadsAsAClaimInDoubt(t *testing.T) {
	err := claudeClaimInDoubt(asRedeemHTTPError(&UsageHTTPError{StatusCode: 503, Body: "overloaded"}))
	if settlementOf(asRedeemHTTPError(&UsageHTTPError{StatusCode: 503})) != claimInDoubt {
		t.Fatal("a 5xx claim was settled")
	}
	if msg := err.Error(); strings.Contains(msg, "usage endpoint") || !strings.Contains(msg, "the redeem request returned HTTP 503") {
		t.Fatalf("err = %q, want it to read as a claim error", msg)
	}
}

// A request the server refuses as such is definite: nothing was spent, and the
// same request would only be refused again.
func TestClaudeClaimRefusedWithAClientErrorIsDefinite(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	ids := claimRecorder(t, `400 {"error":"bad request"}`, `400 {"error":"bad request"}`)
	c := testClaude()
	credit := usage.ResetCredit{ID: "g", Status: "available", Left: 1}
	base := claudeClaimKeyBase(credit)

	_, s, err := c.claim(context.Background(), credit, c.attempts.key(base))
	c.attempts.settle(base, s)
	if err == nil || !strings.Contains(err.Error(), "the redeem request returned HTTP 400") || !strings.Contains(err.Error(), "nothing was spent") {
		t.Fatalf("err = %v, want a refused claim reported as such", err)
	}
	if s != claimRefused {
		t.Fatalf("settlement = %v, want refused", s)
	}
	if _, ok := (pendingClaims{provider: "claude"}).lookup("g", testClaudeOrg, time.Now()); ok {
		t.Fatal("a refused claim was recorded as in doubt")
	}
	if _, _, err := c.claim(context.Background(), credit, c.attempts.key(base)); err == nil {
		t.Fatal("want the second refusal too")
	}
	if len(*ids) != 2 || (*ids)[0] == (*ids)[1] {
		t.Fatalf("request ids = %v, want the next attempt to move on to a new id", *ids)
	}
}

// Timeouts, conflicts and rate limits are about the moment, not the request.
func TestRedeemHTTPErrorRefused(t *testing.T) {
	for status, want := range map[int]bool{
		400: true, 403: true, 404: true, 422: true,
		408: false, 409: false, 425: false, 429: false, 500: false, 503: false,
	} {
		if got := (&RedeemHTTPError{StatusCode: status}).Refused(); got != want {
			t.Fatalf("HTTP %d refused = %t, want %t", status, got, want)
		}
	}
}

func TestClaudeRedeemResetCreditRefusesMalformedGrantID(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		return nil, nil
	})
	for _, id := range []string{"", "Upper", "../evil", strings.Repeat("a", 41)} {
		if _, err := testClaude().RedeemResetCredit(context.Background(), usage.ResetCredit{ID: id}); err == nil {
			t.Fatalf("grant id %q was claimed", id)
		}
	}
}

func TestClaudeOrganizationFallsBackToProfile(t *testing.T) {
	fakeClaudeConfigDir(t, "")
	var claimURL string
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == claudeProfileURL {
			return jsonResponse(req, http.StatusOK, `{"account":{},"organization":{"uuid":"from-profile"}}`), nil
		}
		claimURL = req.URL.String()
		return jsonResponse(req, http.StatusOK, `{"result":"reset"}`), nil
	})
	if _, err := testClaude().RedeemResetCredit(context.Background(), usage.ResetCredit{ID: "g"}); err != nil {
		t.Fatalf("RedeemResetCredit: %v", err)
	}
	if want := "https://api.anthropic.com/api/organizations/from-profile/reset_rate_limits"; claimURL != want {
		t.Fatalf("claim URL = %q, want %q", claimURL, want)
	}
}

// claudeCardsBody is a card read holding one grant "g" with left resets that
// lapses at endsAt, with status-level fields from status (JSON members).
func claudeCardsBody(endsAt time.Time, left int, requiresLimit bool, status string) string {
	if status != "" {
		status += ","
	}
	return fmt.Sprintf(`{
		"five_hour": {"utilization": 100}, "seven_day": {"utilization": 30},
		"cedar_ember": {%s "eligible": true, "next_grant_id": "g", "grants": [{
			"id": "g", "resets_total": 3, "resets_left": %d, "usable_now": true,
			"ends_at": %q, "clears": ["five_hour", "seven_day"], "use_requires_limit": %t
		}]}
	}`, status, left, endsAt.UTC().Format(time.RFC3339), requiresLimit)
}

// claudeAutoRedeemTransport serves card reads with cards() and claims with
// claim, counting the card reads and recording each claim's request id.
func claudeAutoRedeemTransport(t *testing.T, cards func() (int, string), claim string) (cardReads *int, ids *[]string) {
	t.Helper()
	cardReads, ids = new(int), new([]string)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.String() == claudeResetCardsURL:
			*cardReads++
			status, body := cards()
			return jsonResponse(req, status, body), nil
		case req.URL.String() == claudeUsageURL:
			return jsonResponse(req, http.StatusOK, `{"five_hour":{"utilization":100},"seven_day":{"utilization":30}}`), nil
		case req.Method == http.MethodPost:
			var sent map[string]string
			raw, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(raw, &sent)
			*ids = append(*ids, sent["request_id"])
			return jsonResponse(req, http.StatusOK, claim), nil
		}
		t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		return nil, nil
	})
	return cardReads, ids
}

// polled is what an auto-redeem poll hands AutoRedeemResetCredit: the windows,
// and cards carried over from an earlier read.
func polled(credit usage.ResetCredit) *usage.Usage {
	return &usage.Usage{
		FiveHour:     usage.Window{UsedPercent: 100, WindowSeconds: claudeFiveHourSec},
		Weekly:       usage.Window{UsedPercent: 30, WindowSeconds: claudeWeeklySec},
		ResetCredits: &usage.ResetCredits{AvailableCount: credit.Left, Credits: []usage.ResetCredit{credit}},
	}
}

func TestClaudeAutoRedeemSkipsUntilExpiryAndThenThrottles(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	expiresAt := time.Now().Add(30 * time.Minute)
	cardReads, ids := claudeAutoRedeemTransport(t, func() (int, string) {
		return http.StatusOK, claudeCardsBody(expiresAt, 1, false, `"at_limit": false, "exhausted": []`)
	}, `{"result":"not_limited"}`)
	c := testClaude()
	card := func(expiresIn time.Duration) *usage.Usage {
		credit := usage.ResetCredit{ID: "g", Status: "available", Left: 1, Total: 1}
		if expiresIn != 0 {
			credit.ExpiresAt = time.Now().Add(expiresIn)
		}
		return polled(credit)
	}

	for name, u := range map[string]*usage.Usage{"far from expiry": card(10 * 24 * time.Hour), "never expires": card(0)} {
		if res, err := c.AutoRedeemResetCredit(context.Background(), u); res.Outcome != "" || err != nil {
			t.Fatalf("%s: outcome = %q (err %v), want no attempt", name, res, err)
		}
	}
	// Not even a card read: the policy said no on what the poll had.
	if *cardReads != 0 || len(*ids) != 0 {
		t.Fatalf("card reads %d, claims %d; want neither", *cardReads, len(*ids))
	}

	expiring := card(30 * time.Minute)
	if res, err := c.AutoRedeemResetCredit(context.Background(), expiring); res.Outcome != RedeemNothingToReset || err != nil {
		t.Fatalf("outcome = %q (err %v), want %q", res, err, RedeemNothingToReset)
	}
	if res, err := c.AutoRedeemResetCredit(context.Background(), expiring); res.Outcome != "" || err != nil {
		t.Fatalf("outcome = %q (err %v), want the cooldown to suppress the retry", res, err)
	}
	if *cardReads != 1 || len(*ids) != 1 || (*ids)[0] != claudeClaimRequestID(expiring.ResetCredits.Credits[0]) {
		t.Fatalf("card reads %d, request ids %v; want one fresh read and one claim under the card's stable id", *cardReads, *ids)
	}
}

// The poll's cards are up to an hour old, and another process may have spent
// a reset since: the claim is decided, and keyed, on the cards read afresh.
func TestClaudeAutoRedeemDecidesOnFreshCards(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Hour)
	stale := usage.ResetCredit{ID: "g", Status: "available", Left: 3, Total: 3, ExpiresAt: expiresAt, RequiresLimit: true}
	cases := []struct {
		name      string
		cards     string
		wantClaim bool
		wantLeft  int
	}{
		{"another process spent one", claudeCardsBody(expiresAt, 1, true, `"at_limit": true, "exhausted": ["five_hour"]`), true, 1},
		// The windows read 100%, but the server says nothing is exhausted.
		{"server says not at a limit", claudeCardsBody(expiresAt, 3, true, `"at_limit": false, "exhausted": []`), false, 0},
		{"server cooling down", claudeCardsBody(expiresAt, 3, true, `"exhausted": ["five_hour"], "cooldown_until": "2099-01-01T00:00:00Z"`), false, 0},
		{"another process spent the last", claudeCardsBody(expiresAt, 0, true, `"exhausted": ["five_hour"]`), false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClaudeConfigDir(t, testClaudeOrg)
			cardReads, ids := claudeAutoRedeemTransport(t, func() (int, string) { return http.StatusOK, tc.cards }, `{"result":"reset"}`)
			c := testClaude()
			res, err := c.AutoRedeemResetCredit(context.Background(), polled(stale))
			if err != nil {
				t.Fatal(err)
			}
			if *cardReads != 1 {
				t.Fatalf("card reads = %d, want one fresh read before deciding", *cardReads)
			}
			if !tc.wantClaim {
				if res.Outcome != "" || len(*ids) != 0 {
					t.Fatalf("outcome %q, claims %v; want the fresh cards to call it off", res, *ids)
				}
				return
			}
			want := claudeClaimRequestID(usage.ResetCredit{ID: "g", Left: tc.wantLeft})
			if res.Outcome != RedeemReset || len(*ids) != 1 || (*ids)[0] != want {
				t.Fatalf("outcome %q, request ids %v; want one claim keyed on the fresh grant (%s)", res, *ids, want)
			}
		})
	}
}

func TestClaudeAutoRedeemDoesNotClaimWhenTheCardsCannotBeReRead(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	_, ids := claudeAutoRedeemTransport(t, func() (int, string) { return http.StatusBadRequest, `{}` }, `{"result":"reset"}`)
	stale := usage.ResetCredit{ID: "g", Status: "available", Left: 1, ExpiresAt: time.Now().Add(30 * time.Minute)}
	if _, err := testClaude().AutoRedeemResetCredit(context.Background(), polled(stale)); err == nil {
		t.Fatal("want the failed re-read reported")
	}
	if len(*ids) != 0 {
		t.Fatalf("claims = %v, want none on cards that could not be read", *ids)
	}
}

// watch and continue each auto-redeem on their own: a claim one left in doubt
// is repeated by the other, whatever its own attempt count says.
func TestClaudeAutoRedeemRepeatsAnotherProcesssClaimInDoubt(t *testing.T) {
	fakeClaudeConfigDir(t, testClaudeOrg)
	expiresAt := time.Now().Add(30 * time.Minute)
	_, ids := claudeAutoRedeemTransport(t, func() (int, string) {
		return http.StatusOK, claudeCardsBody(expiresAt, 2, false, `"exhausted": []`)
	}, `{"result":"reset"}`)
	if err := (pendingClaims{provider: "claude"}).settle("g",
		pendingClaim{Account: testClaudeOrg, ResetsLeft: 2, RequestID: "from-the-other-process", At: time.Now()}, claimInDoubt); err != nil {
		t.Fatal(err)
	}
	c := testClaude()
	c.attempts.settle(claudeClaimKeyBase(usage.ResetCredit{ID: "g", Left: 2}), claimAnswered) // this process had moved on
	if _, err := c.AutoRedeemResetCredit(context.Background(), polled(usage.ResetCredit{ID: "g", Status: "available", Left: 2, ExpiresAt: expiresAt})); err != nil {
		t.Fatal(err)
	}
	if len(*ids) != 1 || (*ids)[0] != "from-the-other-process" {
		t.Fatalf("request ids = %v, want the claim in doubt repeated", *ids)
	}
}

func TestClaudeClaimRequestIDIsStablePerReset(t *testing.T) {
	first := claudeClaimRequestID(usage.ResetCredit{ID: "g", Left: 2})
	if again := claudeClaimRequestID(usage.ResetCredit{ID: "g", Left: 2}); again != first {
		t.Fatal("the same reset must reuse its request id, so a retried claim cannot spend twice")
	}
	if next := claudeClaimRequestID(usage.ResetCredit{ID: "g", Left: 1}); next == first {
		t.Fatal("the grant's next reset reused the previous request id")
	}
	if other := claudeClaimRequestID(usage.ResetCredit{ID: "h", Left: 2}); other == first {
		t.Fatal("a different grant reused the same request id")
	}
}

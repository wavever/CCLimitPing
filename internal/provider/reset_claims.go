package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

// A reset claim whose outcome is unknown — the backend could not confirm it,
// or the response never arrived — may still land. Retrying it under a new
// request id could then spend a second reset of a multi-reset grant, so the
// request is remembered until a definite answer settles it, and every claim at
// the same reset repeats it instead. Claude Code keeps the same record for the
// same reason.
//
// The record lives on disk, not in memory, because the retry is as likely to
// come from another process — the user re-running `redeem` after an
// unconfirmed claim, or watch and continue both auto-redeeming — as from the
// one that sent it.

// claimSettlement is how far a claim got, which decides what to remember.
type claimSettlement int

const (
	// claimNotSent: the claim never left (a malformed id, no organization);
	// nothing changes.
	claimNotSent claimSettlement = iota
	// claimAnswered: the backend answered definitely, so no reset is in doubt.
	claimAnswered
	// claimRefused: the backend refused the request as such (a client error).
	// Nothing was spent, and repeating the same request would only be refused
	// again, so the next attempt moves on — but an earlier claim that is still
	// in doubt stays remembered: this answer says nothing about it.
	claimRefused
	// claimInDoubt: the claim may have landed; its request must be repeated.
	claimInDoubt
)

// settlementOf classifies a claim that was sent but answered with err.
func settlementOf(err error) claimSettlement {
	var httpErr *RedeemHTTPError
	if errors.As(err, &httpErr) && httpErr.Refused() {
		return claimRefused
	}
	return claimInDoubt
}

// definite reports whether the next attempt at the same reset is a new one.
func (s claimSettlement) definite() bool {
	return s == claimAnswered || s == claimRefused
}

// pendingClaimMaxAge bounds how long a claim stays in doubt. Long past it the
// backend has long since settled the request either way, and a reset read
// afresh is the better guide.
const pendingClaimMaxAge = 7 * 24 * time.Hour

// requestIDRE is the request id shape the backends accept (Claude's, and
// Codex's keys are generated the same way).
var requestIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// pendingClaim is a claim in doubt.
type pendingClaim struct {
	// Account names who sent it (the Claude organization, the ChatGPT account),
	// so a request is never repeated on behalf of another login.
	Account string `json:"account"`
	// ResetsLeft is what the grant held when the claim was sent: if it holds
	// fewer now, the claim most likely landed. Kept for diagnosis; the request
	// is repeated either way, since the backend then answers definitely.
	ResetsLeft int       `json:"resets_left"`
	RequestID  string    `json:"request_id"`
	At         time.Time `json:"at"`
}

type pendingClaimsFile struct {
	// Claims maps the reset's identity (a Claude grant id, a Codex credit's
	// key base) to the claim in doubt for it.
	Claims map[string]pendingClaim `json:"claims"`
}

// pendingClaims is one provider's record of claims in doubt, under
// <config dir>/reset-claims/<provider>.json.
//
// Updates are read-modify-write without a lock: two processes settling claims
// at the same instant could lose one update. Claims are rare and minutes
// apart, and each process's own attempt keys still apply, so a lock would
// guard against very little.
type pendingClaims struct{ provider string }

func (p pendingClaims) path() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "reset-claims", p.provider+".json"), nil
}

func (p pendingClaims) read() pendingClaimsFile {
	var f pendingClaimsFile
	path, err := p.path()
	if err != nil {
		return f
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &f) != nil {
		return pendingClaimsFile{}
	}
	return f
}

// lookup returns the claim in doubt for key on account's behalf, if there is a
// recent one.
func (p pendingClaims) lookup(key, account string, now time.Time) (pendingClaim, bool) {
	c, ok := p.read().Claims[key]
	if !ok || c.Account != account || !requestIDRE.MatchString(c.RequestID) ||
		now.Sub(c.At) > pendingClaimMaxAge || c.At.After(now.Add(time.Hour)) {
		return pendingClaim{}, false
	}
	return c, true
}

// settle records how the claim at key ended: a claim in doubt is remembered, a
// definite answer forgets any claim that was, and the rest leave the record as
// it is.
func (p pendingClaims) settle(key string, claim pendingClaim, s claimSettlement) error {
	f := p.read()
	switch s {
	case claimInDoubt:
		if f.Claims == nil {
			f.Claims = map[string]pendingClaim{}
		}
		f.Claims[key] = claim
	case claimAnswered:
		if _, ok := f.Claims[key]; !ok {
			return nil
		}
		delete(f.Claims, key)
	default:
		return nil
	}
	// Drop what has aged out, so the file never grows past the live claims.
	for k, c := range f.Claims {
		if claim.At.Sub(c.At) > pendingClaimMaxAge {
			delete(f.Claims, k)
		}
	}
	return p.write(f)
}

// write replaces the file atomically, so a crash or a concurrent reader never
// sees half a record.
func (p pendingClaims) write(f pendingClaimsFile) error {
	path, err := p.path()
	if err != nil {
		return err
	}
	if len(f.Claims) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// CreateTemp makes the file 0600: it holds request ids, nobody else's
	// business.
	tmp, err := os.CreateTemp(dir, "."+p.provider+"-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // a no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// claim sends one claim at the reset key names through send, and records how
// it ended. The request id is that of a claim still in doubt for the same
// reset when there is one — so that claim is repeated rather than doubled —
// else fresh. resetsLeft is what the reset's grant holds as far as the caller
// knows, for the record.
func (p pendingClaims) claim(key, account string, resetsLeft int, fresh string,
	send func(requestID string) (RedeemResult, claimSettlement, error)) (RedeemResult, claimSettlement, error) {
	now := time.Now()
	requestID := fresh
	if pending, ok := p.lookup(key, account, now); ok {
		requestID = pending.RequestID
	}
	res, s, err := send(requestID)
	record := pendingClaim{Account: account, ResetsLeft: resetsLeft, RequestID: requestID, At: now}
	if serr := p.settle(key, record, s); serr != nil && s == claimInDoubt {
		err = fmt.Errorf("%w (and the request could not be saved for a safe retry: %v)", err, serr)
	}
	return res, s, err
}

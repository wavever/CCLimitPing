package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// redeemAttempts numbers the logical attempts at spending each reset, so the
// idempotency key of an automatic redemption means "this attempt" rather than
// "this reset".
//
// An attempt whose outcome is unknown — the response was lost, or the backend
// could not confirm — must be retried under the same key, so it cannot spend a
// second reset. But once the backend has definitely answered without spending
// anything (not at a limit, nothing to reset, cooldown, a refused request), the
// next try is a new attempt: a backend that remembers keys would otherwise
// replay the old refusal to every later try, and the reset would lapse unspent.
//
// The count lives in memory. After a restart the first attempt reuses attempt
// zero's key, which at worst replays one refusal before moving on; it can never
// repeat a spend, because a spent reset changes what the key is derived from.
// What must survive a restart — and reach every other process — is a claim in
// doubt, and that is pendingClaims' job: its record overrides the key from
// here, which only applies while no claim at the reset is in doubt.
type redeemAttempts struct {
	mu sync.Mutex
	n  map[string]int
}

// key returns the idempotency key of the current attempt at base, the stable
// identity of the reset being spent. Attempt zero's key is the bare hash of
// base, so keys sent before attempts were counted stay the same.
func (a *redeemAttempts) key(base string) string {
	a.mu.Lock()
	n := a.n[base]
	a.mu.Unlock()
	if n > 0 {
		base = fmt.Sprintf("%s|attempt|%d", base, n)
	}
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:16])
}

// settle records how the attempt at base ended. Only a definite answer — an
// outcome, or a request the backend refused as such — moves on to a new
// attempt; an outcome in doubt leaves the key in place for the retry.
func (a *redeemAttempts) settle(base string, s claimSettlement) {
	if !s.definite() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.n == nil {
		a.n = map[string]int{}
	}
	a.n[base]++
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// newRedeemCmd spends a banked reset credit (a Codex reset credit or a Claude
// reset card). It is a separate, explicit command because redeeming is
// irreversible: `status` only ever reports the credits, and the automatic path
// (auto_redeem) is opt-in.
func newRedeemCmd() *cobra.Command {
	var dryRun, force bool
	text := localizedText()
	cmd := &cobra.Command{
		Use:       "redeem [provider]",
		Aliases:   []string{"r"},
		Short:     text.redeemShort,
		Long:      text.redeemLong,
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"claude", "codex"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ps := enabledProviders(cfg)
			if len(args) == 1 {
				p, err := buildProvider(args[0], cfg)
				if err != nil {
					return err
				}
				ps = []provider.Provider{p}
			}
			return runRedeem(cmd.Context(), cmd.OutOrStdout(), text, ps, dryRun, force)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, text.redeemDryRunFlag)
	cmd.Flags().BoolVar(&force, "force", false, text.redeemForceFlag)
	return cmd
}

// resetRedeemer is a provider that can spend its banked resets.
type resetRedeemer interface {
	provider.Provider
	provider.ResetCreditRedeemer
}

// redeemTimeout bounds one redemption: a network round trip, plus — for Codex —
// starting `codex app-server`, which takes a few seconds.
const redeemTimeout = 90 * time.Second

type redeemPlan struct {
	p      resetRedeemer
	credit usage.ResetCredit
	u      *usage.Usage
}

// runRedeem spends one reset from whichever of ps holds one. When several do,
// it spends nothing and asks for a provider: picking one silently would make an
// irreversible choice on the user's behalf. Unless force is set, it also spends
// nothing while the windows the reset restores are barely used.
func runRedeem(ctx context.Context, out io.Writer, text cliText, ps []provider.Provider, dryRun, force bool) error {
	var plans []redeemPlan
	// Why a provider has nothing to spend, when that is more than "it holds
	// no resets": a failed read, cards it could not read, cards it holds but
	// cannot use right now. Reported only when nothing can be spent anywhere.
	var whyNone []error
	for _, p := range ps {
		r, ok := p.(resetRedeemer)
		if !ok {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		u, err := readUsageWithResetCredits(readCtx, r)
		cancel()
		if err != nil {
			whyNone = append(whyNone, fmt.Errorf("%s: %w", r.Name(), err))
			continue
		}
		if credit, ok := nextRedeemableCredit(u, time.Now()); ok {
			plans = append(plans, redeemPlan{p: r, credit: credit, u: u})
		} else if why := nothingToRedeem(text, r.Name(), u, time.Now()); why != "" {
			whyNone = append(whyNone, errors.New(why))
		}
	}

	switch {
	case len(plans) == 0 && len(whyNone) > 0:
		return errors.Join(whyNone...)
	case len(plans) == 0:
		return fmt.Errorf("%s", text.redeemNoneAvailable)
	case len(plans) > 1 && !dryRun:
		names := make([]string, len(plans))
		for i, pl := range plans {
			names[i] = pl.p.Name()
		}
		return fmt.Errorf(text.redeemPickProviderFmt, strings.Join(names, ", "), names[0])
	}

	if dryRun {
		for _, pl := range plans {
			fmt.Fprint(out, redeemPlanLine(text, pl.p.Name(), pl.credit))
			if !force && lowValue(pl.u, pl.credit) {
				fmt.Fprintf(out, text.redeemLowValueNoteFmt, clearedWindowUsage(text, pl.u, pl.credit))
			}
		}
		fmt.Fprint(out, text.redeemDryRunNote)
		return nil
	}

	pl := plans[0]
	// Some cards (Claude's early-use ones) reset whatever is there, however
	// little, so an unchecked redeem at 5% burns the card for 5%.
	if !force && lowValue(pl.u, pl.credit) {
		return fmt.Errorf(text.redeemLowValueFmt, pl.p.Name(), clearedWindowUsage(text, pl.u, pl.credit))
	}
	fmt.Fprint(out, redeemPlanLine(text, pl.p.Name(), pl.credit))
	// Bounded like the read above: for Codex this drives a `codex app-server`
	// process, and a stuck one must not hang an irreversible command silently.
	redeemCtx, cancel := context.WithTimeout(ctx, redeemTimeout)
	defer cancel()
	res, err := pl.p.RedeemResetCredit(redeemCtx, pl.credit)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, text.redeemOutcomeFmt, pl.p.Name(), redeemOutcomeText(text, res))
	return nil
}

// nothingToRedeem explains why u holds nothing redeem can spend, when that is
// anything but holding no resets at all: the provider withholds them, they
// could not be read, or the ones held cannot be used right now (queued behind
// another card, paused, not usable yet). Empty means there is simply none.
func nothingToRedeem(text cliText, name string, u *usage.Usage, now time.Time) string {
	rc := u.ResetCredits
	if rc != nil && rc.UnavailableReason != "" {
		return fmt.Sprintf(text.redeemCardsUnavailableFmt, name, resetCardsUnavailableText(text, rc.UnavailableReason))
	}
	if u.ResetCreditsError != nil {
		return fmt.Sprintf(text.redeemCardsUnreadFmt, name, u.ResetCreditsError)
	}
	if rc == nil {
		return ""
	}
	var held []string
	seen := map[string]bool{}
	for _, c := range rc.Credits {
		if c.Expired(now) || !c.RedeemedAt.IsZero() || c.Status == "redeemed" || c.Status == "expired" {
			continue
		}
		if word := creditStatusWord(text, c.Status); !seen[word] {
			seen[word] = true
			held = append(held, word)
		}
	}
	if len(held) == 0 {
		return ""
	}
	return fmt.Sprintf(text.redeemNoneUsableFmt, name, strings.Join(held, text.statusListSep))
}

// resetCardsUnavailableText says why the provider offers this caller no reset
// cards. Claude's surface and cli_version reasons mean Anthropic did not take
// limitping for the Claude CLI — usually because `claude --version` could not
// be run, leaving limitping to present an old fallback version — which the
// user can fix, unlike the account-level reasons.
func resetCardsUnavailableText(text cliText, reason string) string {
	if claudeCLIUnrecognized(reason) {
		return fmt.Sprintf(text.cardsUnrecognizedFmt, reason)
	}
	return fmt.Sprintf(text.cardsUnavailableFmt, reason)
}

func claudeCLIUnrecognized(reason string) bool {
	return reason == "surface" || reason == "cli_version"
}

// readUsageWithResetCredits reads usage including the reset credits of a
// provider that leaves them out of ReadUsage (Claude). Only commands the user
// runs to see or spend credits call it; the polling loops never do.
func readUsageWithResetCredits(ctx context.Context, p provider.Provider) (*usage.Usage, error) {
	if r, ok := p.(provider.ResetCreditReader); ok {
		return r.ReadUsageWithResetCredits(ctx)
	}
	return p.ReadUsage(ctx)
}

// clearedWindowUsage renders the usage of the windows c restores, e.g.
// "5h 22%, weekly 4%". A window the provider does not currently enforce is
// left out.
func clearedWindowUsage(text cliText, u *usage.Usage, c usage.ResetCredit) string {
	var parts []string
	for _, w := range u.ClearedUsage(c) {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", clearedWindowNames(text, []string{w.Name}), w.Window.UsedPercent))
	}
	return strings.Join(parts, text.statusListSep)
}

// lowValue reports whether spending c now would visibly reclaim little. A reset
// whose windows the provider reports no reading for is not judged: the user
// asked for it, and there is nothing to warn them with.
func lowValue(u *usage.Usage, c usage.ResetCredit) bool {
	return len(u.ClearedUsage(c)) > 0 && !u.WorthRedeeming(c)
}

// redeemPlanLine names what is about to be spent. The expiry is unknown when
// only a Codex count survived, and absent for a Claude card that never lapses.
func redeemPlanLine(text cliText, name string, c usage.ResetCredit) string {
	label := ""
	if c.Label != "" {
		label = fmt.Sprintf(text.redeemPlanLabelFmt, c.Label)
	}
	expiry := ""
	if !c.ExpiresAt.IsZero() {
		expires := c.ExpiresAt.Local()
		expiry = fmt.Sprintf(text.redeemPlanExpiresFmt,
			expires.Format(text.statusCreditTimeLayout)+" "+fmtZone(expires),
			fmtDurDays(text, time.Until(c.ExpiresAt)))
	}
	return fmt.Sprintf(text.redeemPlanFmt, name, label, expiry)
}

// nextRedeemableCredit returns the soonest-expiring credit that is still
// spendable, ignoring the auto-redeem timing policy: an explicit `redeem` is
// the user asking for it now.
func nextRedeemableCredit(u *usage.Usage, now time.Time) (usage.ResetCredit, bool) {
	if u.ResetCredits == nil {
		return usage.ResetCredit{}, false
	}
	var target usage.ResetCredit
	found := false
	for _, c := range u.ResetCredits.Credits {
		if !c.Redeemable(now) {
			continue
		}
		if !found || c.ExpiresBefore(target) {
			target, found = c, true
		}
	}
	// The Codex detail endpoint is private and may go away, leaving only the
	// count from the usage response; trust that rather than refusing to redeem.
	if !found && u.ResetCredits.AvailableCount > 0 && len(u.ResetCredits.Credits) == 0 {
		return usage.ResetCredit{}, true
	}
	return target, found
}

// redeemOutcomeText puts the backend's answer into words, with its reason
// where that changes what the user should do.
func redeemOutcomeText(text cliText, res provider.RedeemResult) string {
	switch res.Outcome {
	case provider.RedeemReset:
		return text.redeemDone
	case provider.RedeemNothingToReset:
		return text.redeemNothing
	case provider.RedeemNoCredit:
		return text.redeemNoCredit
	case provider.RedeemAlreadyRedeemed:
		return text.redeemAlready
	case provider.RedeemCooldown:
		if !res.RetryAt.IsZero() {
			retry := res.RetryAt.Local()
			return fmt.Sprintf(text.redeemCooldownUntilFmt, retry.Format(text.statusCreditTimeLayout)+" "+fmtZone(retry))
		}
		return text.redeemCooldown
	case provider.RedeemIneligible:
		switch {
		case claudeCLIUnrecognized(res.Reason):
			// Not the card's fault: the claim never got as far as the card.
			return fmt.Sprintf(text.redeemIneligibleUnrecognizedFmt, res.Reason)
		case res.Reason != "":
			return fmt.Sprintf(text.redeemIneligibleReasonFmt, res.Reason)
		}
		return text.redeemIneligible
	default:
		return fmt.Sprintf(text.redeemUnknownFmt, res)
	}
}

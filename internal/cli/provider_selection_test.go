package cli

import (
	"testing"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/scheduler"
)

func TestDefaultConfigDoesNotEnableSpark(t *testing.T) {
	cfg := config.Default()

	providers := enabledProviders(cfg)
	if got, want := providerNames(providers), []string{"claude", "codex"}; !sameStrings(got, want) {
		t.Fatalf("enabled providers = %#v, want %#v", got, want)
	}

	targets, err := buildTargets(cfg)
	if err != nil {
		t.Fatalf("buildTargets: %v", err)
	}
	if got, want := targetNames(targets), []string{"claude", "codex"}; !sameStrings(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestExplicitSparkSelectionWorksWhenDisabled(t *testing.T) {
	cfg := config.Default()

	providers, err := selectProviders(cfg, "spark")
	if err != nil {
		t.Fatalf("selectProviders: %v", err)
	}
	if got, want := providerNames(providers), []string{"spark"}; !sameStrings(got, want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}

	targets, err := selectTargets(cfg, "spark")
	if err != nil {
		t.Fatalf("selectTargets: %v", err)
	}
	if got, want := targetNames(targets), []string{"spark"}; !sameStrings(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestEnabledSparkAppearsInAllSelections(t *testing.T) {
	cfg := config.Default()
	cfg.Spark.Enabled = true

	providers := enabledProviders(cfg)
	if got, want := providerNames(providers), []string{"claude", "codex", "spark"}; !sameStrings(got, want) {
		t.Fatalf("enabled providers = %#v, want %#v", got, want)
	}

	targets, err := buildTargets(cfg)
	if err != nil {
		t.Fatalf("buildTargets: %v", err)
	}
	if got, want := targetNames(targets), []string{"claude", "codex", "spark"}; !sameStrings(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func providerNames(ps []provider.Provider) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name()
	}
	return names
}

func targetNames(targets []scheduler.Target) []string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Provider.Name()
	}
	return names
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

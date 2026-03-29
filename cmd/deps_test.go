package cmd

import (
	"reflect"
	"testing"

	"github.com/velolib/vinth/internal/lockfile"
)

// TestBuildCheckTargets verifies all-target and filtered-target selection behavior.
func TestBuildCheckTargets(t *testing.T) {
	lf := &lockfile.Lockfile{
		Mods: map[string]lockfile.ModEntry{
			"sodium":  {},
			"iris":    {},
			"lithium": {},
		},
	}

	all := buildCheckTargets(lf, nil)
	if len(all) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(all))
	}

	subset := buildCheckTargets(lf, []string{"sodium", "unknown", "sodium", "iris"})
	got := make([]string, 0, len(subset))
	for _, target := range subset {
		got = append(got, target.slug)
	}

	want := []string{"sodium", "iris"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected subset targets: got %v want %v", got, want)
	}
}

// TestCollectDependencyRecheckTargets verifies missing and newly added slugs are merged and sorted.
func TestCollectDependencyRecheckTargets(t *testing.T) {
	missing := map[string][]string{
		"a": {"x"},
		"c": {"y"},
	}
	added := map[string]struct{}{
		"b": {},
		"a": {},
	}

	got := collectDependencyRecheckTargets(missing, added)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected recheck targets: got %v want %v", got, want)
	}
}

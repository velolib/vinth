package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type lockfileState struct {
	Version     int                       `json:"version"`
	GameVersion string                    `json:"game_version"`
	Loader      string                    `json:"loader"`
	Mods        map[string]map[string]any `json:"mods"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to discover test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func buildCLI(t *testing.T, root string) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "vinth-test-bin")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binPath
}

func writeLockfile(t *testing.T, workspace string) {
	t.Helper()
	state := lockfileState{
		Version:     1,
		GameVersion: "1.21.5",
		Loader:      "fabric",
		Mods: map[string]map[string]any{
			"amecs": {
				"project_id":   "abc",
				"version_id":   "v1",
				"file_name":    "amecs.jar",
				"download_url": "https://example.invalid/amecs.jar",
				"sha512_hash":  "h1",
			},
			"appleskin": {
				"project_id":   "def",
				"version_id":   "v2",
				"file_name":    "appleskin.jar",
				"download_url": "https://example.invalid/appleskin.jar",
				"sha512_hash":  "h2",
			},
			"sodium": {
				"project_id":   "ghi",
				"version_id":   "v3",
				"file_name":    "sodium.jar",
				"download_url": "https://example.invalid/sodium.jar",
				"sha512_hash":  "h3",
			},
		},
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal lockfile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "vinth.lock.json"), data, 0o644); err != nil {
		t.Fatalf("write lockfile failed: %v", err)
	}
}

func readLockfile(t *testing.T, workspace string) lockfileState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, "vinth.lock.json"))
	if err != nil {
		t.Fatalf("read lockfile failed: %v", err)
	}
	var state lockfileState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal lockfile failed: %v", err)
	}
	return state
}

// TestRemoveMultipleModsUsesIsolatedWorkspace verifies removing multiple slugs only mutates the isolated temp workspace lockfile.
func TestRemoveMultipleModsUsesIsolatedWorkspace(t *testing.T) {
	root := repoRoot(t)
	bin := buildCLI(t, root)
	workspace := t.TempDir()
	writeLockfile(t, workspace)

	cmd := exec.Command(bin, "remove", "amecs", "appleskin")
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("remove command failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "Removed from vinth.lock.json") {
		t.Fatalf("expected remove output to mention removals, got: %s", output)
	}

	state := readLockfile(t, workspace)
	if _, exists := state.Mods["amecs"]; exists {
		t.Fatalf("expected amecs to be removed")
	}
	if _, exists := state.Mods["appleskin"]; exists {
		t.Fatalf("expected appleskin to be removed")
	}
	if _, exists := state.Mods["sodium"]; !exists {
		t.Fatalf("expected sodium to remain")
	}
}

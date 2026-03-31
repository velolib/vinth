package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/velolib/vinth/internal/api"
)

func withTempCWD(t *testing.T) string {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir temp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
	return tmp
}

// TestSaveLoadRoundTripWithFileSize verifies file_size is persisted and restored across Save/Load.
func TestSaveLoadRoundTripWithFileSize(t *testing.T) {
	withTempCWD(t)

	lf := &Lockfile{
		Version:     CurrentVersion,
		GameVersion: "1.21.5",
		Loader:      "fabric",
		Mods: map[string]ModEntry{
			"sodium": {
				ProjectID:   "AANobbMI",
				VersionID:   "v1",
				VersionName: "v1.0.0",
				VersionLock: true,
				FileName:    "sodium.jar",
				DownloadURL: "https://example.invalid/sodium.jar",
				FileSize:    12345,
				Hash:        "abc",
			},
		},
	}

	if err := lf.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	entry, ok := loaded.Mods["sodium"]
	if !ok {
		t.Fatalf("expected sodium entry")
	}
	if entry.FileSize != 12345 {
		t.Fatalf("expected file size 12345, got %d", entry.FileSize)
	}
	if entry.VersionName != "v1.0.0" {
		t.Fatalf("expected version name v1.0.0, got %s", entry.VersionName)
	}
	if !entry.VersionLock {
		t.Fatalf("expected version lock to be true")
	}
}

// TestLoadLegacyVersionNormalizes verifies lockfiles without version are normalized and migrated to CurrentVersion.
func TestLoadLegacyVersionNormalizes(t *testing.T) {
	tmp := withTempCWD(t)
	originalFetch := fetchVersionByID
	fetchVersionByID = func(versionID string) (*api.ModrinthVersion, error) {
		return &api.ModrinthVersion{ID: versionID, VersionName: "1.0.0+fabric"}, nil
	}
	t.Cleanup(func() {
		fetchVersionByID = originalFetch
	})

	legacy := map[string]any{
		"game_version": "1.20.1",
		"loader":       "fabric",
		"mods": map[string]any{
			"sodium": map[string]any{
				"project_id":   "AANobbMI",
				"version_id":   "old-version-id",
				"file_name":    "sodium.jar",
				"download_url": "https://example.invalid/sodium.jar",
				"sha512_hash":  "hash",
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy lockfile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, LockfileName), data, 0o644); err != nil {
		t.Fatalf("write legacy lockfile failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Version != CurrentVersion {
		t.Fatalf("expected normalized version %d, got %d", CurrentVersion, loaded.Version)
	}
	entry := loaded.Mods["sodium"]
	if entry.VersionName != "1.0.0+fabric" {
		t.Fatalf("expected migrated version_number from API, got %s", entry.VersionName)
	}
	if entry.VersionLock {
		t.Fatalf("expected migrated version_locked to default false")
	}
}

func TestLoadLegacyVersionMigrationFallbackToVersionIDOnAPIFailure(t *testing.T) {
	tmp := withTempCWD(t)
	originalFetch := fetchVersionByID
	fetchVersionByID = func(versionID string) (*api.ModrinthVersion, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() {
		fetchVersionByID = originalFetch
	})

	legacy := map[string]any{
		"game_version": "1.20.1",
		"loader":       "fabric",
		"mods": map[string]any{
			"sodium": map[string]any{
				"project_id":   "AANobbMI",
				"version_id":   "old-version-id",
				"file_name":    "sodium.jar",
				"download_url": "https://example.invalid/sodium.jar",
				"sha512_hash":  "hash",
			},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy lockfile failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, LockfileName), data, 0o644); err != nil {
		t.Fatalf("write legacy lockfile failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	entry := loaded.Mods["sodium"]
	if entry.VersionName != "unknown" {
		t.Fatalf("expected fallback to unknown when API lookup fails, got %s", entry.VersionName)
	}
}

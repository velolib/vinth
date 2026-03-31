// internal/lockfile/lockfile.go
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/velolib/vinth/internal/api"
)

const LockfileName = "vinth.lock.json"
const CurrentVersion = 2

var fetchVersionByID = api.FetchVersionByID

// ModEntry represents a single mod pinned in the lockfile
type ModEntry struct {
	ProjectID   string `json:"project_id"`
	VersionID   string `json:"version_id"`
	VersionName string `json:"version_number"`
	VersionLock bool   `json:"version_locked"`
	FileName    string `json:"file_name"`
	DownloadURL string `json:"download_url"`
	FileSize    int64  `json:"file_size,omitempty"`
	Hash        string `json:"sha512_hash"` // Good for verifying downloads later
}

// Lockfile represents the entire state of the modpack
type Lockfile struct {
	Version     int                 `json:"version"`
	GameVersion string              `json:"game_version"`
	Loader      string              `json:"loader"` // e.g., "fabric" or "forge"
	Mods        map[string]ModEntry `json:"mods"`   // Map key is the mod slug (e.g., "sodium")
}

// normalizeVersion makes legacy lockfiles versioned and validates compatibility.
func (lf *Lockfile) normalizeVersion() error {
	// Legacy lockfiles have no version field, which unmarshals to 0.
	if lf.Version == 0 {
		lf.Version = 1
	}

	if lf.Version < 0 {
		return fmt.Errorf("invalid lockfile version: %d", lf.Version)
	}

	if lf.Version > CurrentVersion {
		return fmt.Errorf("unsupported lockfile version %d (current supported version is %d)", lf.Version, CurrentVersion)
	}

	if lf.Mods == nil {
		lf.Mods = make(map[string]ModEntry)
	}

	if lf.Version < CurrentVersion {
		lf.migrateToCurrentVersion()
	}

	return nil
}

func (lf *Lockfile) migrateToCurrentVersion() {
	if lf.Version < 2 {
		updatedMods := make(map[string]ModEntry, len(lf.Mods))
		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, 8)

		for slug, entry := range lf.Mods {
			wg.Add(1)
			go func(modSlug string, modEntry ModEntry) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				if modEntry.VersionName == "" {
					modEntry.VersionName = resolveMigratedVersionName(modEntry)
				}

				mu.Lock()
				updatedMods[modSlug] = modEntry
				mu.Unlock()
			}(slug, entry)
		}

		wg.Wait()
		lf.Mods = updatedMods
		lf.Version = 2
	}
}

func resolveMigratedVersionName(entry ModEntry) string {
	if entry.VersionName != "" {
		return entry.VersionName
	}

	if entry.VersionID == "" {
		return "unknown"
	}

	versionInfo, err := fetchVersionByID(entry.VersionID)
	if err != nil || versionInfo == nil {
		return "unknown"
	}

	if versionInfo.VersionName != "" {
		return versionInfo.VersionName
	}

	if versionInfo.ID != "" {
		return versionInfo.ID
	}

	return "unknown"
}

// Load reads the lockfile from disk, or returns an empty one if it doesn't exist
func Load() (*Lockfile, error) {
	data, err := os.ReadFile(LockfileName)
	if err != nil {
		if os.IsNotExist(err) {
			// Return a default empty lockfile
			return &Lockfile{
				Version: CurrentVersion,
				Mods:    make(map[string]ModEntry),
			}, nil
		}
		return nil, err
	}

	var lf Lockfile
	err = json.Unmarshal(data, &lf)
	if err != nil {
		return nil, err
	}
	loadedVersion := lf.Version
	fromVersion := loadedVersion
	if fromVersion == 0 {
		fromVersion = 1
	}
	migrating := fromVersion < CurrentVersion
	if migrating {
		fmt.Fprintf(os.Stderr, "ℹ️  Migrating vinth.lock.json from version %d to %d...\n", fromVersion, CurrentVersion)
	}

	if err := lf.normalizeVersion(); err != nil {
		return nil, err
	}

	if migrating {
		if err := lf.Save(); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, "✅ Migration complete.")
	}

	return &lf, nil
}

// Save writes the current state of the lockfile to disk
func (lf *Lockfile) Save() error {
	if err := lf.normalizeVersion(); err != nil {
		return err
	}

	// MarshalIndent makes the JSON pretty and readable
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(LockfileName)
	tmpFile, err := os.CreateTemp(dir, LockfileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, LockfileName); err != nil {
		if removeErr := os.Remove(LockfileName); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if retryErr := os.Rename(tmpPath, LockfileName); retryErr != nil {
			return retryErr
		}
	}

	return nil
}

func Exists() bool {
	_, err := os.Stat(LockfileName)
	return err == nil
}

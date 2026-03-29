// internal/lockfile/lockfile.go
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const LockfileName = "vinth.lock.json"
const CurrentVersion = 1

// ModEntry represents a single mod pinned in the lockfile
type ModEntry struct {
	ProjectID   string `json:"project_id"`
	VersionID   string `json:"version_id"`
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
		lf.Version = CurrentVersion
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

	return nil
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

	if err := lf.normalizeVersion(); err != nil {
		return nil, err
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

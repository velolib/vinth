// internal/download/download.go
package download

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/velolib/vinth/internal/api"
	"github.com/velolib/vinth/internal/errors"
)

// SecureFile downloads a file to a .part extension, verifies its hash, and renames it.
func SecureFile(url string, destPath string, expectedHash string) error {
	// 1. Create the .part file
	partPath := destPath + ".part"
	out, err := os.Create(partPath)
	if err != nil {
		return errors.New("download.SecureFile", "file", fmt.Errorf("could not create temp file: %w", err))
	}
	closed := false
	closeOut := func() {
		if !closed {
			out.Close()
			closed = true
		}
	}

	// 2. Setup the HTTP client with a generous timeout for large files
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return errors.New("download.SecureFile", "network", err)
	}
	// Reuse the same identity string used by other API calls.
	req.Header.Set("User-Agent", api.APIUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		os.Remove(partPath) // Cleanup on failure
		return errors.New("download.SecureFile", "network", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Remove(partPath)
		return errors.New("download.SecureFile", "network", fmt.Errorf("bad status: %s", resp.Status))
	}

	// 3. Setup the hasher.
	// io.TeeReader writes to the file AND the hasher at the exact same time
	// so we don't have to read the file twice!
	hasher := sha512.New()
	reader := io.TeeReader(resp.Body, hasher)

	// 4. Stream the download to the disk
	if _, err := io.Copy(out, reader); err != nil {
		closeOut()
		os.Remove(partPath)
		return errors.New("download.SecureFile", "io", fmt.Errorf("download interrupted: %w", err))
	}

	// 5. Verify the Hash
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != expectedHash {
		closeOut()
		os.Remove(partPath)
		return errors.New("download.SecureFile", "hash", fmt.Errorf("hash mismatch! Expected %s, got %s", expectedHash, actualHash))
	}

	// Windows requires the destination file to be closed before renaming
	closeOut()

	// 6. Atomic Rename: If we got here, the file is 100% perfect.
	// On Windows, os.Rename fails if destPath exists. Remove it first if needed.
	if _, err := os.Stat(destPath); err == nil {
		// File exists, try to remove
		if err := os.Remove(destPath); err != nil {
			return errors.New("download.SecureFile", "file", fmt.Errorf("could not remove existing file before rename: %w", err))
		}
	}
	if err := os.Rename(partPath, destPath); err != nil {
		return errors.New("download.SecureFile", "file", fmt.Errorf("could not finalize file rename: %w", err))
	}

	return nil
}

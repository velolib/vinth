package utils

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SanitizeModFileName rejects path-like values and only allows a plain file name.
func SanitizeModFileName(fileName string) (string, error) {
	trimmed := strings.TrimSpace(fileName)
	if trimmed == "" {
		return "", fmt.Errorf("file name is empty")
	}

	clean := filepath.Clean(trimmed)
	base := filepath.Base(clean)
	if base == "." || base == ".." || base == "" {
		return "", fmt.Errorf("invalid file name: %q", fileName)
	}

	if clean != base {
		return "", fmt.Errorf("unsafe file name (path components are not allowed): %q", fileName)
	}

	return base, nil
}

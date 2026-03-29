package cmd

import "testing"

// TestPickerHeightDefaultsInNonTTY verifies default and clamped heights when no TTY size is available.
func TestPickerHeightDefaultsInNonTTY(t *testing.T) {
	if got := pickerHeight(1000); got != 6 {
		t.Fatalf("expected default non-tty height 6 for large option count, got %d", got)
	}

	if got := pickerHeight(2); got != 2 {
		t.Fatalf("expected height to clamp to option count for small lists, got %d", got)
	}

	if got := pickerHeight(0); got != 6 {
		t.Fatalf("expected baseline height 6 for zero options, got %d", got)
	}
}

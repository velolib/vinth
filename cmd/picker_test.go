package cmd

import "testing"

// TestPickerHeightDefaultsInNonTTY verifies default and clamped heights when no TTY size is available.
func TestPickerHeightDefaultsInNonTTY(t *testing.T) {
	// Non-TTY defaults: 24 terminal rows - 3 buffer = 21 available.
	// Large lists cap visible options to 18 plus 2 chrome rows => 20.
	if got := pickerHeight(1000); got != 20 {
		t.Fatalf("expected default non-tty height 20 for large option count, got %d", got)
	}

	// Small lists still reserve enough room for 3 visible options + chrome.
	if got := pickerHeight(2); got != 5 {
		t.Fatalf("expected minimum usable height 5 for small lists, got %d", got)
	}

	if got := pickerHeight(0); got != 5 {
		t.Fatalf("expected baseline height 5 for zero options, got %d", got)
	}
}

package ui

import "testing"

func TestAvailable_NoSPA(t *testing.T) {
	// In M1 dist/ only contains .gitkeep, so Available() returns false.
	if Available() {
		t.Error("Available() = true, want false (no SPA built yet)")
	}
}

func TestFS_ErrorsWhenEmpty(t *testing.T) {
	if _, err := FS(); err == nil {
		t.Error("FS() returned nil error with empty dist/")
	}
}

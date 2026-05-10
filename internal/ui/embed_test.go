package ui

import (
	"io/fs"
	"testing"
)

// distHasIndex reports whether the embedded dist/ tree contains an index.html
// file (i.e. `make ui` has actually populated it). The unit tests must be
// robust to either state because the embed is part of the source tree:
// CI runs from a clean checkout (only .gitkeep present), but a developer
// who has run `make dev` will have a full SPA in the embed. Asserting one
// fixed answer divorces the tests from the actual artifact and produces
// the local-vs-CI divergence we explicitly want to avoid.
func distHasIndex(t *testing.T) bool {
	t.Helper()
	entries, err := distFS.ReadDir("dist")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == "index.html" {
			return true
		}
	}
	return false
}

func TestAvailable_MatchesDiskState(t *testing.T) {
	want := distHasIndex(t)
	if got := Available(); got != want {
		t.Errorf("Available() = %v, want %v (based on real dist/ contents)", got, want)
	}
}

func TestFS_ContractMatchesAvailability(t *testing.T) {
	hasIndex := distHasIndex(t)
	sub, err := FS()
	switch {
	case hasIndex && err != nil:
		t.Errorf("FS() returned error %v despite dist/index.html existing", err)
	case !hasIndex && err == nil:
		t.Errorf("FS() returned nil error with empty dist/")
	case hasIndex && err == nil:
		// Sanity-check the returned subtree: index.html should be readable.
		if _, ferr := fs.ReadFile(sub, "index.html"); ferr != nil {
			t.Errorf("FS().ReadFile(index.html) failed: %v", ferr)
		}
	}
}

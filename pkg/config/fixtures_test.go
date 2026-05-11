package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShippedFixtureConfigsLoad guards every YAML under configs/ that
// ships with the binary. A merge that introduces a duplicate map key
// (the most common merge-resolution hazard for the per-group toggle
// section) causes yaml.v3's strict decoder to reject the file at
// Load time. Without this test no other target in `make verify` loads
// the fixtures, so the breakage slips through to first boot.
//
// Loads with APITEST_INSECURE set so the example config's
// skip_signature_verification field doesn't trip the secret-mode gate.
// The same env vars referenced by ${VAR:-default} interpolation get
// populated with safe stand-ins.
func TestShippedFixtureConfigsLoad(t *testing.T) {
	repoRoot := findRepoRoot(t)
	configsDir := filepath.Join(repoRoot, "configs")

	entries, err := os.ReadDir(configsDir)
	if err != nil {
		t.Fatalf("read configs dir: %v", err)
	}

	// Provide values for the ${VAR:-default} placeholders that the
	// fixtures reference. Values are dummy but pass the load-time
	// validators (which only check shape, not content).
	// Future-proofing: if a fixture flips skip_signature_verification
	// to true, Load gates on this env var. Setting it here avoids a
	// silent re-failure after such a change.
	t.Setenv("APITEST_INSECURE", "1")
	t.Setenv("APITEST_COOKIE_SECRET", "test-cookie-secret-32-bytes-long!!")
	t.Setenv("APITEST_DEV_KEY", "test-dev-key")
	t.Setenv("APITEST_DEV_BEARER", "test-dev-bearer")
	t.Setenv("APITEST_OIDC_ISSUER", "http://localhost:8081/realms/api-test")
	t.Setenv("APITEST_OIDC_AUDIENCE", "api-test")
	t.Setenv("PLEXARA_ADMIN_URL", "http://localhost:9000/api/v1/admin/api-gateway/connections")
	t.Setenv("PLEXARA_ADMIN_AUTH", "")

	loaded := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "api-test.") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path := filepath.Join(configsDir, name)
		t.Run(name, func(t *testing.T) {
			if _, err := Load(path); err != nil {
				t.Fatalf("config.Load(%s) failed: %v", path, err)
			}
		})
		loaded++
	}
	if loaded == 0 {
		t.Fatalf("no api-test.*.yaml fixtures found under %s", configsDir)
	}
}

// findRepoRoot walks up from the current test working directory until
// it finds a directory containing a go.mod whose module path matches
// the project. Simpler than passing in a path; the test binary's CWD
// is the package directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", dir)
		}
		dir = parent
	}
}

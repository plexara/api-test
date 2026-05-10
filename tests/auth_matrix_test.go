//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestIntegration_AuthMatrix walks every supported inbound credential
// type the M2 chain knows about against /v1/whoami:
//   - no credential                          → 401 + WWW-Authenticate
//   - wrong api key                          → 401
//   - X-API-Key (header)                     → 200, auth_type=apikey
//   - api_key (query)                        → 200, auth_type=apikey
//   - Authorization: Bearer (static token)   → 200, auth_type=bearer
//   - Authorization: Bearer (wrong token)    → 401
func TestIntegration_AuthMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pgURL := startPostgres(ctx, t)
	url, _ := boot(t, pgURL)

	cases := []struct {
		name       string
		setup      func(*http.Request)
		wantStatus int
		wantAuth   string
		wantSubj   string
	}{
		{
			name:       "no credential",
			setup:      func(*http.Request) {},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong api key",
			setup: func(r *http.Request) {
				r.Header.Set("X-API-Key", "wrong")
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "X-API-Key header",
			setup: func(r *http.Request) {
				r.Header.Set("X-API-Key", TestAPIKey)
			},
			wantStatus: http.StatusOK,
			wantAuth:   "apikey",
			wantSubj:   "intkey",
		},
		{
			name: "api_key query",
			setup: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("api_key", TestAPIKey)
				r.URL.RawQuery = q.Encode()
			},
			wantStatus: http.StatusOK,
			wantAuth:   "apikey",
			wantSubj:   "intkey",
		},
		{
			name: "Bearer static token",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+TestBearerToken)
			},
			wantStatus: http.StatusOK,
			wantAuth:   "bearer",
			wantSubj:   "intbearer",
		},
		{
			name: "Bearer wrong",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer wrongness")
			},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, url+"/v1/whoami", nil)
			if err != nil {
				t.Fatal(err)
			}
			tc.setup(req)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status %d want %d body=%s", resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Bearer") {
					t.Errorf("WWW-Authenticate missing: %q", resp.Header.Get("WWW-Authenticate"))
				}
				return
			}

			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["auth_type"] != tc.wantAuth {
				t.Errorf("auth_type=%v want %s", body["auth_type"], tc.wantAuth)
			}
			if body["subject"] != tc.wantSubj {
				t.Errorf("subject=%v want %s", body["subject"], tc.wantSubj)
			}
		})
	}
}

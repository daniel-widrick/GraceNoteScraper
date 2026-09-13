package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderClientFindProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/Providers/getPostalCodeProviders/USA/11743/gapzap/en-us"
		if r.URL.Path != wantPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
			t.Fatalf("X-Requested-With = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
          "DSTUTCOffset":"-240",
          "Providers":[{
            "type":"CABLE",
            "device":"X",
            "lineupId":"USA-NY67791-DEFAULT",
            "name":"Verizon Fios - Digital",
            "location":"Huntington",
            "postalCode":"11743",
            "headendId":"NY67791"
          }]
        }`))
	}))
	defer server.Close()

	client := newProviderClient(server.Client(), server.URL)
	result, err := client.FindProviders(context.Background(), "usa", "11743", "EN-US")
	if err != nil {
		t.Fatalf("FindProviders() error = %v", err)
	}
	if len(result.Providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(result.Providers))
	}
	provider := result.Providers[0]
	if provider.LineupID != "USA-NY67791-DEFAULT" || provider.HeadendID != "NY67791" {
		t.Fatalf("unexpected provider: %+v", provider)
	}
}

func TestProviderClientRejectsNonSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer server.Close()

	client := newProviderClient(server.Client(), server.URL)
	_, err := client.FindProviders(context.Background(), "USA", "11743", "en-us")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("FindProviders() error = %v, want status error", err)
	}
}

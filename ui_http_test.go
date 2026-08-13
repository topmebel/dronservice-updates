package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationPagesUseSharedVisualTheme(t *testing.T) {
	pages := map[string]string{
		"devices":    devicesPageHTML,
		"ip-cameras": ipCamerasPageHTML,
		"streams":    streamsPageHTML,
		"zerotier":   zeroTierPageHTML,
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(page, `<link rel="stylesheet" href="/assets/application.css">`) {
				t.Fatal("page does not load the shared visual theme")
			}
		})
	}
}

func TestApplicationStyleHandlerServesLocalAccessibleTheme(t *testing.T) {
	response := httptest.NewRecorder()
	applicationStyleHandler(response, httptest.NewRequest(http.MethodGet, "/assets/application.css", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	for _, fragment := range []string{
		`button:focus-visible`,
		`width:44px!important`,
		`height:24px!important`,
		`border-radius:999px!important`,
		`flex:0 0 44px!important`,
		`@media(max-width:600px)`,
		`@media(prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("theme does not contain %q", fragment)
		}
	}
}

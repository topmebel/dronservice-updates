package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"DronService/internal/mediamtx"
)

func TestStreamsPageProvidesManualMediaMTXConfigEditor(t *testing.T) {
	for _, fragment := range []string{"edit-mediamtx-config", "mediamtx-config-editor", "/api/mediamtx/config-file", "method:'PUT'", "Редактировать конфигурацию"} {
		if !strings.Contains(streamsPageHTML, fragment) {
			t.Errorf("streams page does not contain %q", fragment)
		}
	}
}

func TestMediaMTXConfigFileHandlerReadsAndWritesRawConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mediamtx.yml")
	original := "# header\npaths:\n  all_others:\n"
	if err := os.WriteFile(path, []byte(original), 0o660); err != nil {
		t.Fatal(err)
	}
	handler := mediaMTXConfigFileHandler(mediamtx.NewConfigFile(path))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/mediamtx/config-file", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"content":"# header\npaths:`)) {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}

	updated := "# changed header\nlogLevel: debug\npaths:\n  all_others:\n"
	body, _ := json.Marshal(map[string]string{"content": updated})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/mediamtx/config-file", bytes.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", response.Code, response.Body.String())
	}
	content, _ := os.ReadFile(path)
	if string(content) != updated {
		t.Fatalf("saved config = %q, want %q", content, updated)
	}
}

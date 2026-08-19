package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"DronService/internal/buildinfo"
	"DronService/internal/updater"
)

func TestVersionHandlerReturnsBuildMetadata(t *testing.T) {
	previous := buildinfo.Version
	buildinfo.Version = "v0.1.0"
	t.Cleanup(func() { buildinfo.Version = previous })
	response := httptest.NewRecorder()
	versionHandler(response, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var info buildinfo.Info
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "v0.1.0" || info.Arch == "" {
		t.Fatalf("version response = %+v", info)
	}
}

func TestUpdateStatusIsDisabledWithoutRepository(t *testing.T) {
	client, err := updater.NewClient(updater.Config{CurrentVersion: "v0.1.0", RequestPath: filepath.Join(t.TempDir(), "request")})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	applicationUpdateStatusHandler(client).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/update/status", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"disabledReason":"repository-not-configured"`) {
		t.Fatalf("response = %s", response.Body.String())
	}
}

func TestApplicationStatusScriptUsesManualUpdateOnly(t *testing.T) {
	for _, fragment := range []string{
		`button.addEventListener('click'`,
		`fetch('/api/update',{method:'POST'})`,
		`confirm('Установить новую версию DronService?`,
		`button.hidden=!status.updateAvailable||status.installing`,
		`repository-not-configured`,
		`status.message?(stateLabels[status.state]`,
		`updateNetworkInfo()`,
		`networkAddresses.innerHTML`,
	} {
		if !strings.Contains(applicationStatusScript, fragment) {
			t.Errorf("application status script does not contain %q", fragment)
		}
	}
	if strings.Contains(applicationStatusScript, "setInterval") {
		t.Fatal("application status script must not automatically start updates")
	}
	if strings.Contains(applicationStatusScript, "Перезагрузить Starlink") {
		t.Fatal("Starlink reboot must only be available on the Starlink page")
	}
	if strings.Contains(applicationStatusScript, "/api/system/starlink/reboot") {
		t.Fatal("application status script must not call Starlink reboot API")
	}
}

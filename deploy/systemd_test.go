package deploy

import (
	"os"
	"strings"
	"testing"
)

func TestDronServiceUnitProvidesSafeRuntimeDefaults(t *testing.T) {
	data, err := os.ReadFile("dronservice.service")
	if err != nil {
		t.Fatalf("read dronservice.service: %v", err)
	}
	unit := string(data)
	required := []string{
		"After=network-online.target mediamtx.service",
		"Wants=network-online.target mediamtx.service",
		"StartLimitIntervalSec=60",
		"StartLimitBurst=5",
		"StateDirectory=dronservice",
		"StateDirectoryMode=0750",
		"TimeoutStopSec=15s",
		"UMask=0077",
		"SyslogIdentifier=dronservice",
		"EnvironmentFile=-/etc/dronservice/update.conf",
	}
	for _, setting := range required {
		if !strings.Contains(unit, setting) {
			t.Errorf("dronservice.service does not contain %q", setting)
		}
	}
}

func TestApplicationUpdateIsManualAndRunsInSeparateService(t *testing.T) {
	pathUnit := readDeploymentFile(t, "dronservice-update.path")
	for _, setting := range []string{
		"PathExists=/var/lib/dronservice/update-dronservice.request",
		"Unit=dronservice-update.service",
	} {
		if !strings.Contains(pathUnit, setting) {
			t.Errorf("dronservice-update.path does not contain %q", setting)
		}
	}
	serviceUnit := readDeploymentFile(t, "dronservice-update.service")
	for _, setting := range []string{
		"Type=oneshot",
		"ExecStart=/usr/local/libexec/dronservice-update",
		"EnvironmentFile=/etc/dronservice/update.conf",
		"RuntimeDirectory=dronservice-update",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
	} {
		if !strings.Contains(serviceUnit, setting) {
			t.Errorf("dronservice-update.service does not contain %q", setting)
		}
	}
	if _, err := os.Stat("dronservice-update.timer"); !os.IsNotExist(err) {
		t.Fatal("automatic update timer must not be installed")
	}
}

func TestApplicationUpdaterVerifiesAndRollsBackRelease(t *testing.T) {
	script := readDeploymentFile(t, "update-dronservice.sh")
	for _, fragment := range []string{
		`https://github.com/${DRONSERVICE_UPDATE_REPOSITORY}/releases/download/${version}`,
		`while [ "$attempt" -le 10 ]`,
		`sha256sum --check --status`,
		`openssl dgst -sha256 -verify`,
		`--version`,
		`systemctl restart dronservice.service`,
		`rollback`,
		`/api/health`,
		`/api/version`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("update-dronservice.sh does not contain %q", fragment)
		}
	}
}

func readDeploymentFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

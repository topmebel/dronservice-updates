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

func TestMediaMTXDeploymentEnablesHLSPreview(t *testing.T) {
	for _, name := range []string{"mediamtx.yml", "install-mediamtx.sh"} {
		content := readDeploymentFile(t, name)
		for _, fragment := range []string{"hls: yes", "hlsAddress: :8888"} {
			if !strings.Contains(content, fragment) {
				t.Errorf("%s does not contain %q", name, fragment)
			}
		}
	}
}

func TestMediaMTXConfigurationIsEditableByDronService(t *testing.T) {
	units := readDeploymentFile(t, "mediamtx.service") + readDeploymentFile(t, "dronservice.service")
	for _, fragment := range []string{
		"/usr/local/etc/mediamtx/mediamtx.yml",
		"MEDIAMTX_CONFIG_PATH=/usr/local/etc/mediamtx/mediamtx.yml",
		"ReadWritePaths=/var/lib/dronservice /usr/local/etc/mediamtx",
	} {
		if !strings.Contains(units, fragment) {
			t.Errorf("systemd configuration does not contain %q", fragment)
		}
	}
	installer := readDeploymentFile(t, "install-mediamtx.sh")
	for _, fragment := range []string{"chown admin:admin", "chmod 0660", "/usr/local/etc/mediamtx.yml"} {
		if !strings.Contains(installer, fragment) {
			t.Errorf("MediaMTX installer does not contain migration fragment %q", fragment)
		}
	}
}

func TestAnalogVideoRuntimeHasExplicitProvisioningScript(t *testing.T) {
	script := readDeploymentFile(t, "install-video-runtime.sh")
	for _, fragment := range []string{
		`if [ "$(id -u)" -ne 0 ]`,
		"command -v apt-get",
		"apt-get update",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg",
		"[ -x /usr/bin/ffmpeg ]",
		"/usr/bin/ffmpeg -hide_banner -version",
		"/usr/bin/ffmpeg -hide_banner -encoders",
		"/usr/bin/ffmpeg -hide_banner -demuxers",
		"/usr/bin/ffmpeg -hide_banner -muxers",
		"libx264",
		"video4linux2,v4l2",
		"rtsp",
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("install-video-runtime.sh does not contain %q", fragment)
		}
	}
	info, err := os.Stat("install-video-runtime.sh")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("install-video-runtime.sh is not executable")
	}
}

func TestMediaMTXServiceRunsHooksWithoutRoot(t *testing.T) {
	unit := readDeploymentFile(t, "mediamtx.service")
	for _, fragment := range []string{"User=admin", "Group=admin", "SupplementaryGroups=video", "AmbientCapabilities=CAP_NET_BIND_SERVICE", "NoNewPrivileges=true"} {
		if !strings.Contains(unit, fragment) {
			t.Errorf("mediamtx.service does not contain %q", fragment)
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

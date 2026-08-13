package buildinfo

import "testing"

func TestCurrentReturnsInjectedBuildMetadata(t *testing.T) {
	previousVersion, previousCommit, previousBuiltAt := Version, Commit, BuiltAt
	Version, Commit, BuiltAt = "v1.2.3", "abc123", "2026-08-13T12:00:00Z"
	t.Cleanup(func() { Version, Commit, BuiltAt = previousVersion, previousCommit, previousBuiltAt })

	info := Current()
	if info.Version != "v1.2.3" || info.Commit != "abc123" || info.BuiltAt != "2026-08-13T12:00:00Z" {
		t.Fatalf("Current() = %+v", info)
	}
	if info.OS == "" || info.Arch == "" || info.GoVersion == "" {
		t.Fatalf("runtime metadata is incomplete: %+v", info)
	}
}

func TestValidVersion(t *testing.T) {
	for _, version := range []string{"v0.1.0", "v10.20.30"} {
		if !ValidVersion(version) {
			t.Errorf("ValidVersion(%q) = false", version)
		}
	}
	for _, version := range []string{"dev", "1.2.3", "v1.2", "v1.2.3-beta", "v1.2.3;reboot"} {
		if ValidVersion(version) {
			t.Errorf("ValidVersion(%q) = true", version)
		}
	}
}

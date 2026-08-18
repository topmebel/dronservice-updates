package mediamtx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFilePathChangesPreserveHeaderAndOtherPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mediamtx.yml")
	header := "################################\n# custom header\nlogLevel: debug\n\npaths:\n"
	original := header + "  camera_old:\n    source: 'rtsp://old'\n  # unrelated path comment\n  camera_newer:\n    source: 'rtsp://newer'\n  all_others:\n    source: publisher\n"
	if err := os.WriteFile(path, []byte(original), 0o660); err != nil {
		t.Fatal(err)
	}
	config := NewConfigFile(path)
	if err := config.SetPath("camera_new", PathConfigUpdate{Source: "rtsp://admin:p'ass@192.168.1.20/main", SourceOnDemand: true}); err != nil {
		t.Fatal(err)
	}
	updated, err := config.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updated, header) || !strings.Contains(updated, "  camera_old:\n    source: 'rtsp://old'\n") || !strings.Contains(updated, "  # unrelated path comment\n  camera_newer:\n    source: 'rtsp://newer'\n") || !strings.Contains(updated, "  all_others:\n    source: publisher\n") {
		t.Fatalf("unrelated configuration changed:\n%s", updated)
	}
	if !strings.Contains(updated, "source: 'rtsp://admin:p''ass@192.168.1.20/main'") {
		t.Fatalf("managed path was not safely rendered:\n%s", updated)
	}
	if err := config.DeletePath("camera_new"); err != nil {
		t.Fatal(err)
	}
	final, _ := config.Read()
	if final != original {
		t.Fatalf("add/delete did not restore exact original\nwant:\n%s\ngot:\n%s", original, final)
	}
}

func TestConfigFileManualWriteRequiresPathsSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mediamtx.yml")
	if err := os.WriteFile(path, []byte("paths:\n  all_others:\n"), 0o660); err != nil {
		t.Fatal(err)
	}
	config := NewConfigFile(path)
	if err := config.Write("logLevel: debug\n"); err == nil {
		t.Fatal("Write() accepted configuration without paths section")
	}
	if err := config.Write("paths:\n  camera: [unterminated\n"); err == nil {
		t.Fatal("Write() accepted invalid YAML")
	}
	want := "# retained manually\nlogLevel: debug\npaths:\n  all_others:\n"
	if err := config.Write(want); err != nil {
		t.Fatal(err)
	}
	got, _ := config.Read()
	if got != want {
		t.Fatalf("Read() = %q, want %q", got, want)
	}
}

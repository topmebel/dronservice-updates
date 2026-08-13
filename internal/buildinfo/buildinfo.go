package buildinfo

import (
	"regexp"
	"runtime"
)

var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"builtAt"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func Current() Info {
	return Info{Version: Version, Commit: Commit, BuiltAt: BuiltAt, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func ValidVersion(version string) bool {
	return versionPattern.MatchString(version)
}

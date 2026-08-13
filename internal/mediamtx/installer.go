package mediamtx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	latestVersionCacheTTL      = 15 * time.Minute
	latestVersionErrorCacheTTL = time.Minute
)

type Installer struct {
	binaryPath, requestPath string
	http                    *http.Client
	latestMu                sync.Mutex
	latestVersionCached     string
	latestVersionErr        error
	latestVersionExpiresAt  time.Time
}

type InstallStatus struct {
	Installed       bool   `json:"installed"`
	Installing      bool   `json:"installing"`
	Version         string `json:"version,omitempty"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

func NewInstaller(binaryPath, requestPath string) *Installer {
	return &Installer{binaryPath: binaryPath, requestPath: requestPath, http: &http.Client{Timeout: 5 * time.Second}}
}

func (i *Installer) Status(ctx context.Context) (InstallStatus, error) {
	installed, err := fileExists(i.binaryPath)
	if err != nil {
		return InstallStatus{}, fmt.Errorf("check MediaMTX binary: %w", err)
	}
	installing, err := fileExists(i.requestPath)
	if err != nil {
		return InstallStatus{}, fmt.Errorf("check MediaMTX install request: %w", err)
	}
	status := InstallStatus{Installed: installed, Installing: installing}
	if installed {
		out, err := exec.CommandContext(ctx, i.binaryPath, "--version").Output()
		fields := strings.Fields(string(out))
		if err == nil && len(fields) > 0 {
			status.Version = strings.TrimPrefix(fields[len(fields)-1], "v")
		}
	}
	latest, err := i.latestVersionForStatus(ctx)
	if err == nil {
		status.LatestVersion = strings.TrimPrefix(latest, "v")
		status.UpdateAvailable = status.Installed && status.Version != status.LatestVersion
	}
	return status, nil
}

func (i *Installer) latestVersionForStatus(ctx context.Context) (string, error) {
	i.latestMu.Lock()
	defer i.latestMu.Unlock()

	if time.Now().Before(i.latestVersionExpiresAt) {
		return i.latestVersionCached, i.latestVersionErr
	}
	version, err := i.latestVersion(ctx)
	if ctx.Err() != nil {
		return version, err
	}
	ttl := latestVersionCacheTTL
	if err != nil {
		ttl = latestVersionErrorCacheTTL
	}
	i.latestVersionCached = version
	i.latestVersionErr = err
	i.latestVersionExpiresAt = time.Now().Add(ttl)
	return version, err
}

func (i *Installer) Request(ctx context.Context) error {
	if exists, err := fileExists(i.requestPath); err != nil || exists {
		return err
	}
	version, err := i.latestVersion(ctx)
	if err != nil {
		return fmt.Errorf("get latest MediaMTX version: %w", err)
	}
	file, err := os.OpenFile(i.requestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create MediaMTX install request: %w", err)
	}
	if _, err = file.WriteString(version + "\n"); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (i *Installer) latestVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/bluenviron/mediamtx/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	if !strings.HasPrefix(release.Tag, "v") {
		return "", fmt.Errorf("invalid release tag")
	}
	return release.Tag, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

package zerotier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Updater struct {
	requestPath string
	http        *http.Client
}

func NewUpdater(requestPath string) *Updater {
	return &Updater{requestPath: requestPath, http: &http.Client{Timeout: 5 * time.Second}}
}

func (u *Updater) Enrich(ctx context.Context, snapshot *Snapshot) {
	snapshot.Updating, _ = exists(u.requestPath)
	if output, err := exec.CommandContext(ctx, "zerotier-one", "-v").Output(); err == nil {
		snapshot.Installed = true
		if snapshot.Status.Version == "" {
			snapshot.Status.Version = strings.TrimSpace(string(output))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/zerotier/ZeroTierOne/releases/latest", nil)
	if err != nil {
		return
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var release struct {
		Tag string `json:"tag_name"`
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&release) == nil {
		snapshot.LatestVersion = strings.TrimPrefix(release.Tag, "v")
		snapshot.UpdateAvailable = snapshot.Installed && snapshot.Status.Version != "" && snapshot.Status.Version != snapshot.LatestVersion
	}
}

func (u *Updater) Request() error {
	file, err := os.OpenFile(u.requestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create ZeroTier update request: %w", err)
	}
	return file.Close()
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

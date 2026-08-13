package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"DronService/internal/buildinfo"
)

const (
	StateIdle        = "idle"
	StatePending     = "pending"
	StateDownloading = "downloading"
	StateVerifying   = "verifying"
	StateInstalling  = "installing"
	StateRestarting  = "restarting"
	StateSucceeded   = "succeeded"
	StateFailed      = "failed"

	releaseCacheTTL      = 15 * time.Minute
	releaseErrorCacheTTL = time.Minute
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Config struct {
	Repository     string
	CurrentVersion string
	RequestPath    string
	StatusPath     string
}

type Client struct {
	repository, currentVersion, requestPath, statusPath string
	http                                                *http.Client
	apiBase                                             string
	mu                                                  sync.Mutex
	cachedRelease                                       release
	cachedErr                                           error
	cacheExpiresAt                                      time.Time
}

type Status struct {
	Enabled         bool   `json:"enabled"`
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Installing      bool   `json:"installing"`
	State           string `json:"state"`
	TargetVersion   string `json:"targetVersion,omitempty"`
	ReleaseNotes    string `json:"releaseNotes,omitempty"`
	ReleaseURL      string `json:"releaseUrl,omitempty"`
	CheckFailed     bool   `json:"checkFailed,omitempty"`
	Message         string `json:"message,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

type release struct {
	Tag  string
	URL  string
	Body string
}

type persistedStatus struct {
	State     string `json:"state"`
	Version   string `json:"version,omitempty"`
	Message   string `json:"message,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

func NewClient(config Config) (*Client, error) {
	repository := strings.TrimSpace(config.Repository)
	if repository != "" && !repositoryPattern.MatchString(repository) {
		return nil, fmt.Errorf("invalid GitHub update repository")
	}
	return &Client{
		repository:     repository,
		currentVersion: strings.TrimSpace(config.CurrentVersion),
		requestPath:    config.RequestPath,
		statusPath:     config.StatusPath,
		http:           &http.Client{Timeout: 8 * time.Second},
		apiBase:        "https://api.github.com",
	}, nil
}

func (c *Client) Status(ctx context.Context) Status {
	status := Status{Enabled: c.repository != "", CurrentVersion: c.currentVersion, State: StateIdle}
	if persisted, ok := c.readPersistedStatus(); ok {
		status.State = persisted.State
		status.TargetVersion = persisted.Version
		status.Message = persisted.Message
		status.UpdatedAt = persisted.UpdatedAt
	}
	if exists(c.requestPath) {
		status.State = StatePending
		status.Installing = true
	}
	if isActiveState(status.State) {
		status.Installing = true
	}
	if !status.Enabled {
		return status
	}

	release, err := c.latestReleaseCached(ctx)
	if err != nil {
		status.CheckFailed = true
		return status
	}
	status.LatestVersion = release.Tag
	status.ReleaseNotes = release.Body
	status.ReleaseURL = release.URL
	status.UpdateAvailable = newerVersion(release.Tag, c.currentVersion) && !status.Installing
	return status
}

func (c *Client) Request(ctx context.Context) error {
	status := c.Status(ctx)
	if !status.Enabled {
		return fmt.Errorf("application updates are not configured")
	}
	if status.Installing {
		return nil
	}
	if status.CheckFailed {
		return fmt.Errorf("latest release is unavailable")
	}
	if !status.UpdateAvailable || !buildinfo.ValidVersion(status.LatestVersion) {
		return fmt.Errorf("no application update is available")
	}
	file, err := os.CreateTemp(filepath.Dir(c.requestPath), ".update-dronservice-request-*")
	if err != nil {
		return fmt.Errorf("create temporary application update request: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if _, err := file.WriteString(status.LatestVersion + "\n"); err != nil {
		file.Close()
		return fmt.Errorf("write application update request: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync application update request: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close application update request: %w", err)
	}
	if err := os.Link(temporaryPath, c.requestPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("publish application update request: %w", err)
	}
	return nil
}

func (c *Client) latestReleaseCached(ctx context.Context) (release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.cacheExpiresAt) {
		return c.cachedRelease, c.cachedErr
	}
	latest, err := c.latestRelease(ctx)
	if ctx.Err() != nil {
		return release{}, err
	}
	ttl := releaseCacheTTL
	if err != nil {
		ttl = releaseErrorCacheTTL
	}
	c.cachedRelease, c.cachedErr, c.cacheExpiresAt = latest, err, time.Now().Add(ttl)
	return latest, err
}

func (c *Client) latestRelease(ctx context.Context) (release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/repos/"+c.repository+"/releases/latest", nil)
	if err != nil {
		return release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "DronService/"+c.currentVersion)
	response, err := c.http.Do(request)
	if err != nil {
		return release{}, fmt.Errorf("request latest GitHub release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Tag    string `json:"tag_name"`
		URL    string `json:"html_url"`
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return release{}, fmt.Errorf("decode latest GitHub release: %w", err)
	}
	if !buildinfo.ValidVersion(payload.Tag) {
		return release{}, fmt.Errorf("invalid GitHub release version")
	}
	required := map[string]bool{
		"dronservice-linux-arm64":     false,
		"checksums.sha256":            false,
		"dronservice-linux-arm64.sig": false,
	}
	for _, asset := range payload.Assets {
		if _, expected := required[asset.Name]; expected {
			required[asset.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			return release{}, fmt.Errorf("GitHub release does not contain %s", name)
		}
	}
	return release{Tag: payload.Tag, URL: payload.URL, Body: payload.Body}, nil
}

func (c *Client) readPersistedStatus() (persistedStatus, bool) {
	data, err := os.ReadFile(c.statusPath)
	if err != nil {
		return persistedStatus{}, false
	}
	var status persistedStatus
	if json.Unmarshal(data, &status) != nil || !validState(status.State) {
		return persistedStatus{}, false
	}
	return status, true
}

func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func isActiveState(state string) bool {
	switch state {
	case StatePending, StateDownloading, StateVerifying, StateInstalling, StateRestarting:
		return true
	default:
		return false
	}
}

func validState(state string) bool {
	switch state {
	case StateIdle, StatePending, StateDownloading, StateVerifying, StateInstalling, StateRestarting, StateSucceeded, StateFailed:
		return true
	default:
		return false
	}
}

func newerVersion(candidate, current string) bool {
	left, leftOK := versionParts(candidate)
	right, rightOK := versionParts(current)
	if !leftOK || !rightOK {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return left[index] > right[index]
		}
	}
	return false
}

func versionParts(version string) ([3]int, bool) {
	var result [3]int
	if !buildinfo.ValidVersion(version) {
		return result, false
	}
	for index, part := range strings.Split(strings.TrimPrefix(version, "v"), ".") {
		value, err := strconv.Atoi(part)
		if err != nil {
			return result, false
		}
		result[index] = value
	}
	return result, true
}

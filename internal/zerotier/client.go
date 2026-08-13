package zerotier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const maxResponseSize = 1 << 20

type Status struct {
	Address           string `json:"address"`
	Online            bool   `json:"online"`
	Version           string `json:"version"`
	TCPFallbackActive bool   `json:"tcpFallbackActive"`
}

type Network struct {
	ID                string   `json:"id"`
	NWID              string   `json:"nwid,omitempty"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	Type              string   `json:"type"`
	PortDeviceName    string   `json:"portDeviceName"`
	AssignedAddresses []string `json:"assignedAddresses"`
	AllowManaged      bool     `json:"allowManaged"`
	AllowGlobal       bool     `json:"allowGlobal"`
	AllowDefault      bool     `json:"allowDefault"`
	AllowDNS          bool     `json:"allowDNS"`
}

type Snapshot struct {
	Status          Status    `json:"status"`
	Networks        []Network `json:"networks"`
	Installed       bool      `json:"installed"`
	Available       bool      `json:"available"`
	LatestVersion   string    `json:"latestVersion,omitempty"`
	UpdateAvailable bool      `json:"updateAvailable"`
	Updating        bool      `json:"updating"`
}

type Client struct {
	baseURL   *url.URL
	tokenFile string
	http      *http.Client
}

func NewClient(baseURL, tokenFile string) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://127.0.0.1:9993"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid ZeroTier API URL")
	}
	return &Client{
		baseURL:   parsed,
		tokenFile: tokenFile,
		http:      &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var snapshot Snapshot
	if err := c.request(ctx, http.MethodGet, "/status", nil, &snapshot.Status); err != nil {
		return Snapshot{}, fmt.Errorf("get ZeroTier status: %w", err)
	}
	if err := c.request(ctx, http.MethodGet, "/network", nil, &snapshot.Networks); err != nil {
		return Snapshot{}, fmt.Errorf("list ZeroTier networks: %w", err)
	}
	if snapshot.Networks == nil {
		snapshot.Networks = []Network{}
	}
	for index := range snapshot.Networks {
		if snapshot.Networks[index].ID == "" {
			snapshot.Networks[index].ID = snapshot.Networks[index].NWID
		}
	}
	return snapshot, nil
}

func (c *Client) Join(ctx context.Context, networkID string) error {
	return c.request(ctx, http.MethodPost, "/network/"+networkID, []byte(`{}`), nil)
}

func (c *Client) Leave(ctx context.Context, networkID string) error {
	return c.request(ctx, http.MethodDelete, "/network/"+networkID, nil, nil)
}

func (c *Client) request(ctx context.Context, method, endpoint string, body []byte, output any) error {
	tokenBytes, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return fmt.Errorf("read ZeroTier API token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return fmt.Errorf("ZeroTier API token is empty")
	}
	requestURL := *c.baseURL
	requestURL.Path = path.Join(strings.TrimSuffix(c.baseURL.Path, "/"), endpoint)
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ZeroTier API request: %w", err)
	}
	request.Header.Set("X-ZT1-Auth", token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request ZeroTier API: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseSize)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, limited)
		return fmt.Errorf("ZeroTier API returned HTTP %d", response.StatusCode)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if err := json.NewDecoder(limited).Decode(output); err != nil {
		return fmt.Errorf("decode ZeroTier API response: %w", err)
	}
	return nil
}

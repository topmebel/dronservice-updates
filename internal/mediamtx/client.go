package mediamtx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBodySize = 10 << 20

// HTTPError identifies a non-success response from the MediaMTX Control API.
// Callers can make narrow status-specific decisions without parsing error text.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("MediaMTX returned HTTP %d", e.StatusCode)
}

func IsHTTPStatus(err error, statusCode int) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == statusCode
}

type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Client) ListPaths(ctx context.Context) ([]Path, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/v3/paths/list",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create MediaMTX request: %w", err)
	}

	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request MediaMTX: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
	if err != nil {
		return nil, fmt.Errorf("read MediaMTX response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}

	var pathsResponse PathsResponse
	if err := json.Unmarshal(body, &pathsResponse); err != nil {
		return nil, fmt.Errorf("decode MediaMTX paths response: %w", err)
	}

	return pathsResponse.Items, nil
}

func (c *Client) ListConfigPaths(ctx context.Context) ([]ConfigPath, error) {
	var response ConfigPathsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v3/config/paths/list", nil, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) AddConfigPath(ctx context.Context, name string, update PathConfigUpdate) error {
	return c.doJSON(ctx, http.MethodPost, "/v3/config/paths/add/"+url.PathEscape(name), update, nil)
}

func (c *Client) PatchConfigPath(ctx context.Context, name string, update PathConfigUpdate) error {
	return c.doJSON(ctx, http.MethodPatch, "/v3/config/paths/patch/"+url.PathEscape(name), update, nil)
}

func (c *Client) DeleteConfigPath(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, "/v3/config/paths/delete/"+url.PathEscape(name), nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode MediaMTX request: %w", err)
		}
		body = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create MediaMTX request: %w", err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request MediaMTX: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
	if err != nil {
		return fmt.Errorf("read MediaMTX response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode}
	}
	if output != nil && len(responseBody) != 0 {
		if err := json.Unmarshal(responseBody, output); err != nil {
			return fmt.Errorf("decode MediaMTX response: %w", err)
		}
	}
	return nil
}

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var internetChecker = newConnectivityChecker([]string{
	"https://connectivitycheck.gstatic.com/generate_204",
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://github.com",
})

const (
	connectivityOnline  = "online"
	connectivityOffline = "offline"
	connectivityUnknown = "unknown"
)

type internetStatusResponse struct {
	Online    bool      `json:"online"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checkedAt"`
}

type networkStatusResponse struct {
	LAN       []string `json:"lan"`
	WiFi      []string `json:"wifi"`
	LocalName string   `json:"localName,omitempty"`
}

type networkInterfaceSnapshot struct {
	Name      string
	Flags     net.Flags
	Addresses []net.Addr
}

type connectivityChecker struct {
	client    *http.Client
	endpoints []string
	mu        sync.Mutex
	online    bool
	status    string
	confirmed bool
	failures  int
	checkedAt time.Time
}

func newConnectivityChecker(endpoints []string) *connectivityChecker {
	return &connectivityChecker{
		client: &http.Client{
			Timeout: 4 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoints: endpoints,
		status:    connectivityUnknown,
	}
}

func (c *connectivityChecker) Online(ctx context.Context) bool {
	return c.Status(ctx).Online
}

func (c *connectivityChecker) Status(ctx context.Context) internetStatusResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	cacheDuration := 3 * time.Second
	if c.online {
		cacheDuration = 10 * time.Second
	}
	if time.Since(c.checkedAt) < cacheDuration {
		return c.response()
	}

	checkCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan bool, len(c.endpoints))
	for _, endpoint := range c.endpoints {
		go func() {
			request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, endpoint, nil)
			if err != nil {
				results <- false
				return
			}
			response, err := c.client.Do(request)
			if err != nil {
				results <- false
				return
			}
			response.Body.Close()
			results <- response.StatusCode >= 200 && response.StatusCode < 500
		}()
	}

	reachable := false
	for range c.endpoints {
		if <-results {
			reachable = true
			cancel()
			break
		}
	}
	if reachable {
		c.online = true
		c.status = connectivityOnline
		c.confirmed = true
		c.failures = 0
	} else {
		c.failures++
		c.status = connectivityUnknown
		if c.failures >= 3 {
			c.online = false
			c.status = connectivityOffline
		}
	}
	c.checkedAt = time.Now()
	return c.response()
}

func (c *connectivityChecker) response() internetStatusResponse {
	return internetStatusResponse{Online: c.online, Status: c.status, CheckedAt: c.checkedAt}
}

func internetStatusHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, internetChecker.Status(r.Context()))
}

func networkStatusHandler(w http.ResponseWriter, r *http.Request) {
	snapshots, err := networkInterfaceSnapshots()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "network interfaces unavailable"})
		return
	}
	hostname, _ := os.Hostname()
	localName := strings.TrimSuffix(strings.TrimSpace(hostname), ".")
	if localName != "" && !strings.HasSuffix(strings.ToLower(localName), ".local") {
		localName += ".local"
	}
	if localName != "" && !localNameAvailable(r.Context(), localName) {
		localName = ""
	}
	writeJSON(w, http.StatusOK, buildNetworkStatus(snapshots, localName))
}

func networkInterfaceSnapshots() ([]networkInterfaceSnapshot, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	snapshots := make([]networkInterfaceSnapshot, 0, len(interfaces))
	for _, iface := range interfaces {
		addresses, addressErr := iface.Addrs()
		if addressErr != nil {
			continue
		}
		snapshots = append(snapshots, networkInterfaceSnapshot{Name: iface.Name, Flags: iface.Flags, Addresses: addresses})
	}
	return snapshots, nil
}

func buildNetworkStatus(interfaces []networkInterfaceSnapshot, localName string) networkStatusResponse {
	status := networkStatusResponse{LAN: []string{}, WiFi: []string{}, LocalName: localName}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		target := &status.LAN
		switch {
		case strings.HasPrefix(name, "wl"):
			target = &status.WiFi
		case strings.HasPrefix(name, "eth"), strings.HasPrefix(name, "en"):
		default:
			continue
		}
		for _, address := range iface.Addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
				continue
			}
			*target = append(*target, ip.String())
		}
	}
	sort.Strings(status.LAN)
	sort.Strings(status.WiFi)
	return status
}

func localNameAvailable(ctx context.Context, name string) bool {
	if info, err := os.Stat("/run/avahi-daemon/socket"); err == nil && info.Mode()&os.ModeSocket != 0 {
		return true
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupHost(lookupCtx, name)
	return err == nil && len(addresses) > 0
}

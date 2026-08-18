package cameraproxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidTarget = errors.New("invalid camera proxy target")
	ErrClosed        = errors.New("camera proxy manager is closed")
)

type Target struct {
	ID            string
	Address       string
	ClientAddress string
	HTTPPort      uint16
}

type Result struct {
	Mode      string
	Address   string
	ExpiresAt time.Time
	DirectURL string
}

type Diagnostic struct {
	PiIPs        []string `json:"piIPs"`
	CameraIP     string   `json:"cameraIP"`
	Interface    string   `json:"interface,omitempty"`
	Route        string   `json:"route"`
	ConnectError string   `json:"connectError,omitempty"`
}

type ConnectivityError struct {
	Diagnostic Diagnostic
	Cause      error
}

func (e *ConnectivityError) Error() string { return fmt.Sprintf("camera connectivity: %v", e.Cause) }
func (e *ConnectivityError) Unwrap() error { return e.Cause }

type Config struct {
	ListenAddress string
	TTL           time.Duration
	LocalNetworks func() ([]*net.IPNet, error)
	DialContext   func(context.Context, string, string) (net.Conn, error)
	RouteLookup   func(net.IP) (string, string, error)
	allowLoopback bool
}

type Manager struct {
	mu       sync.Mutex
	config   Config
	sessions map[string]*session
	closed   bool
}

type session struct {
	server    *http.Server
	listener  net.Listener
	target    string
	expiresAt time.Time
	timer     *time.Timer
}

func NewManager(config Config) *Manager {
	if config.ListenAddress == "" {
		config.ListenAddress = ":0"
	}
	if config.TTL <= 0 {
		config.TTL = 15 * time.Minute
	}
	if config.LocalNetworks == nil {
		config.LocalNetworks = interfaceNetworks
	}
	if config.DialContext == nil {
		config.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	if config.RouteLookup == nil {
		config.RouteLookup = routeLookup
	}
	return &Manager{config: config, sessions: make(map[string]*session)}
}

func (m *Manager) Start(target Target) (Result, error) {
	ip := net.ParseIP(strings.TrimSpace(target.Address))
	if target.ID == "" || ip == nil || ip.To4() == nil || (!m.config.allowLoopback && ip.IsLoopback()) || ip.IsUnspecified() || ip.IsMulticast() {
		return Result{}, ErrInvalidTarget
	}
	port := target.HTTPPort
	if port == 0 {
		port = 80
	}
	targetURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))}
	networks, err := m.config.LocalNetworks()
	if err != nil {
		return Result{}, fmt.Errorf("list local networks: %w", err)
	}
	clientIP := net.ParseIP(strings.TrimSpace(target.ClientAddress))
	for _, network := range networks {
		if clientIP != nil && network.Contains(ip) && network.Contains(clientIP) {
			return Result{Mode: "direct", DirectURL: targetURL.String() + "/"}, nil
		}
	}
	diagnostic := Diagnostic{CameraIP: ip.String(), Route: "unavailable"}
	for _, network := range networks {
		diagnostic.PiIPs = append(diagnostic.PiIPs, network.IP.String())
	}
	localIP, interfaceName, routeErr := m.config.RouteLookup(ip)
	if routeErr != nil {
		diagnostic.ConnectError = safeNetworkError(routeErr)
		return Result{}, &ConnectivityError{Diagnostic: diagnostic, Cause: routeErr}
	}
	diagnostic.Route = "via " + localIP
	diagnostic.Interface = interfaceName
	connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	connection, connectErr := m.config.DialContext(connectCtx, "tcp", targetURL.Host)
	cancel()
	if connectErr != nil {
		diagnostic.ConnectError = safeNetworkError(connectErr)
		return Result{}, &ConnectivityError{Diagnostic: diagnostic, Cause: connectErr}
	}
	_ = connection.Close()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Result{}, ErrClosed
	}
	if current := m.sessions[target.ID]; current != nil {
		if current.target == targetURL.String() {
			expiresAt := time.Now().Add(m.config.TTL)
			current.expiresAt = expiresAt
			current.timer.Stop()
			current.timer = time.AfterFunc(m.config.TTL, func() { m.stop(target.ID, current, expiresAt) })
			return Result{Mode: "proxy", Address: current.listener.Addr().String(), ExpiresAt: expiresAt}, nil
		}
		delete(m.sessions, target.ID)
		current.timer.Stop()
		_ = current.server.Close()
	}

	listener, err := net.Listen("tcp", m.config.ListenAddress)
	if err != nil {
		return Result{}, fmt.Errorf("listen for camera proxy: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
		log.Printf("camera setup proxy target=%s: %v", target.ID, proxyErr)
		http.Error(w, "camera web interface unavailable", http.StatusBadGateway)
	}

	expiresAt := time.Now().Add(m.config.TTL)
	server := &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	current := &session{server: server, listener: listener, target: targetURL.String(), expiresAt: expiresAt}
	current.timer = time.AfterFunc(m.config.TTL, func() { m.stop(target.ID, current, expiresAt) })
	m.sessions[target.ID] = current
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("serve camera setup proxy target=%s: %v", target.ID, serveErr)
		}
		m.remove(target.ID, current)
	}()
	return Result{Mode: "proxy", Address: listener.Addr().String(), ExpiresAt: expiresAt}, nil
}

func safeNetworkError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	for _, known := range []string{"no route to host", "network is unreachable", "connection refused", "i/o timeout", "operation timed out"} {
		if strings.Contains(message, known) {
			return known
		}
	}
	return "TCP connection failed"
}

func routeLookup(target net.IP) (string, string, error) {
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: target, Port: 9})
	if err != nil {
		return "", "", err
	}
	localIP := connection.LocalAddr().(*net.UDPAddr).IP
	_ = connection.Close()
	interfaces, err := net.Interfaces()
	if err != nil {
		return localIP.String(), "", err
	}
	for _, networkInterface := range interfaces {
		addresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil && ip.Equal(localIP) {
				return localIP.String(), networkInterface.Name, nil
			}
		}
	}
	return localIP.String(), "", nil
}

func (m *Manager) stop(id string, expected *session, expiresAt time.Time) {
	m.mu.Lock()
	current := m.sessions[id]
	if current == expected && current.expiresAt.Equal(expiresAt) {
		delete(m.sessions, id)
	} else {
		current = nil
	}
	m.mu.Unlock()
	if current != nil {
		_ = current.server.Close()
	}
}

func (m *Manager) remove(id string, expected *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[id] == expected {
		delete(m.sessions, id)
	}
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for id, current := range m.sessions {
		delete(m.sessions, id)
		current.timer.Stop()
		sessions = append(sessions, current)
	}
	m.mu.Unlock()
	var errs []error
	for _, current := range sessions {
		if err := current.server.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func interfaceNetworks() ([]*net.IPNet, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	networks := make([]*net.IPNet, 0, len(addresses))
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() {
			networks = append(networks, network)
		}
	}
	return networks, nil
}

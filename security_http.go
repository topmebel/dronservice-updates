package main

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type HTTPAccessConfig struct {
	Username string
	Password string
}

func secureHTTPHandler(next http.Handler, config HTTPAccessConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		if !requestIsLoopback(r) && !validBasicAuth(r, config) {
			w.Header().Set("WWW-Authenticate", `Basic realm="DronService", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if changesState(r.Method) && !requestIsLoopback(r) && !sameOriginRequest(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-src http: https:; connect-src 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
}

func validBasicAuth(r *http.Request, config HTTPAccessConfig) bool {
	if config.Username == "" || config.Password == "" {
		return false
	}
	username, password, ok := r.BasicAuth()
	return ok && constantTimeEqual(username, config.Username) && constantTimeEqual(password, config.Password)
}

func constantTimeEqual(value, expected string) bool {
	if len(value) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

func requestIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func changesState(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func sameOriginRequest(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme != "" && strings.EqualFold(parsed.Host, r.Host)
}

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRequest(r) || !hasAllowedOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "local requests only"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackRequest(r *http.Request) bool {
	if r == nil || hasForwardingHeaders(r.Header) || !isLoopbackHost(r.Host) {
		return false
	}
	remoteHost, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		remoteHost = strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
	}
	ip := net.ParseIP(remoteHost)
	return strings.EqualFold(remoteHost, "localhost") || (ip != nil && ip.IsLoopback())
}

func isLoopbackHost(authority string) bool {
	host := hostname(authority)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostname(authority string) string {
	authority = strings.TrimSpace(authority)
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(authority, "[]")
}

func hasForwardingHeaders(header http.Header) bool {
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if strings.TrimSpace(header.Get(name)) != "" {
			return true
		}
	}
	return false
}

func hasAllowedOrigin(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		return true
	}
	origin, err := url.Parse(raw)
	if err != nil || origin == nil || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	if !isLoopbackHost(origin.Host) {
		return false
	}
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	return canonicalAuthority(origin.Host, origin.Scheme) == canonicalAuthority(r.Host, requestScheme)
}

func canonicalAuthority(authority, scheme string) string {
	host := strings.ToLower(hostname(authority))
	port := ""
	if _, parsedPort, err := net.SplitHostPort(strings.TrimSpace(authority)); err == nil {
		port = parsedPort
	}
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

func validateLoopbackListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fmt.Errorf("invalid web listen address %q: %w", addr, err)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("invalid web listen address %q: port is required", addr)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("web listen address must be loopback-only: %s", addr)
	}
	return nil
}

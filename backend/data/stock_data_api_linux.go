//go:build linux
// +build linux

package data

import (
	"os"

	appconfig "go-stock/internal/config"
)

// CheckChrome checks whether a chromium-compatible browser exists on Linux.
func CheckChrome() (string, bool) {
	if path := appconfig.Load().Browser.Path; path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}

	locations := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
	}
	for _, location := range locations {
		if _, err := os.Stat(location); err == nil {
			return location, true
		}
	}
	return "", false
}

// CheckBrowser checks whether a browser path can be used by chromedp.
func CheckBrowser() (string, bool) {
	return CheckChrome()
}

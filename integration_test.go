package main

import (
	"os"
	"testing"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("skip integration test; set RUN_INTEGRATION_TESTS=1 to enable")
	}
}

func requireDesktopTest(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_DESKTOP_TESTS") != "1" {
		t.Skip("skip desktop test; set RUN_DESKTOP_TESTS=1 to enable")
	}
}

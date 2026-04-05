//go:build darwin
// +build darwin

package data

import (
	"go-stock/backend/logger"
	"testing"

	"github.com/go-toast/toast"
)

// @Author 2lovecode
// @Date 2025/02/06 17:50
// @Desc
// -----------------------------------------------------------------------------------

func TestAlert(t *testing.T) {
	requireIntegration(t)
	notification := toast.Notification{
		AppID:    "go-stock",
		Title:    "Hello, World!",
		Message:  "This is a toast notification.",
		Icon:     "../../build/appicon.png",
		Duration: "short",
		Audio:    toast.Default,
	}
	err := notification.Push()
	if err != nil {
		logger.SugaredLogger.Error(err)
		return
	}
}

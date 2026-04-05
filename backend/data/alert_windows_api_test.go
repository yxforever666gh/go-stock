//go:build windows
// +build windows

package data

import (
	"go-stock/backend/logger"
	"testing"

	"github.com/go-toast/toast"
)

// @Author spark
// @Date 2025/1/8 9:40
// @Desc
//-----------------------------------------------------------------------------------

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

//go:build !webonly
// +build !webonly

package main

import (
	"context"

	"go-stock/backend/logger"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) registerCommonRuntimeEvents(ctx context.Context) {
	runtime.EventsOn(ctx, "frontendError", func(optionalData ...interface{}) {
		logger.SugaredLogger.Errorf("Frontend error: %v\n", optionalData)
	})

	runtime.EventsOn(ctx, "updateSettings", func(optionalData ...interface{}) {
		a.reloadWindowTheme(ctx)
	})
}

func (a *App) reloadWindowTheme(ctx context.Context) {
	config := a.services.Config.GetConfig()
	if config == nil {
		return
	}
	if config.DarkTheme {
		runtime.WindowSetBackgroundColour(ctx, 27, 38, 54, 1)
		runtime.WindowSetDarkTheme(ctx)
	} else {
		runtime.WindowSetBackgroundColour(ctx, 255, 255, 255, 1)
		runtime.WindowSetLightTheme(ctx)
	}
	runtime.WindowReloadApp(ctx)
}

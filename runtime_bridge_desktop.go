//go:build !webonly
// +build !webonly

package main

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type runtimeFileFilter struct {
	DisplayName string
	Pattern     string
}

type runtimeSaveFileOptions struct {
	Title                string
	DefaultFilename      string
	CanCreateDirectories bool
	Filters              []runtimeFileFilter
}

func emitDesktopEvent(ctx context.Context, eventName string, payload any) {
	runtime.EventsEmit(ctx, eventName, payload)
}

func saveFileWithDialog(ctx context.Context, options runtimeSaveFileOptions) (string, error) {
	filters := make([]runtime.FileFilter, 0, len(options.Filters))
	for _, filter := range options.Filters {
		filters = append(filters, runtime.FileFilter{
			DisplayName: filter.DisplayName,
			Pattern:     filter.Pattern,
		})
	}
	return runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
		Title:                options.Title,
		DefaultFilename:      options.DefaultFilename,
		CanCreateDirectories: options.CanCreateDirectories,
		Filters:              filters,
	})
}

func openExternalURL(ctx context.Context, url string) {
	runtime.BrowserOpenURL(ctx, url)
}

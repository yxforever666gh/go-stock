//go:build webonly
// +build webonly

package main

import (
	"context"
	"errors"
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

func emitDesktopEvent(_ context.Context, _ string, _ any) {
}

func saveFileWithDialog(_ context.Context, _ runtimeSaveFileOptions) (string, error) {
	return "", errors.New("webonly 模式不支持桌面保存文件对话框，请使用浏览器下载")
}

func openExternalURL(_ context.Context, _ string) {
}

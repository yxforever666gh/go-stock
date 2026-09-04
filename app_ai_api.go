package main

import (
	"encoding/base64"
	"strings"
)

type versionInfoResponse struct {
	Version string `json:"version"`
	Content string `json:"content"`
	Icon    string `json:"icon"`
}

func (a *App) versionInfo() *versionInfoResponse {
	content := VersionCommit
	if strings.TrimSpace(content) == "" {
		content = "development build"
	}
	return &versionInfoResponse{
		Version: Version,
		Icon:    imageBase(icon),
		Content: content,
	}
}

func imageBase(bytes []byte) string {
	if len(bytes) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(bytes)
}

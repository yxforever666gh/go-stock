//go:build darwin
// +build darwin

package data

import "os"

// CheckChrome 检查 macOS 是否安装了 Chrome 浏览器
func CheckChrome() (string, bool) {
	// 检查 /Applications 目录下是否存在 Chrome
	locations := []string{
		// Mac
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	path := ""
	for _, location := range locations {
		_, err := os.Stat(location)
		if err != nil {
			continue
		}
		path = location
	}
	if path == "" {
		return "", false
	}

	return path, true
}

// CheckBrowser 检查 macOS 是否安装了浏览器，并返回安装路径
func CheckBrowser() (string, bool) {
	if path, ok := CheckChrome(); ok {
		return path, ok
	}
	return "", false
}

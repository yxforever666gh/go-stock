//go:build windows
// +build windows

package data

import "golang.org/x/sys/windows/registry"

// CheckChrome 在 Windows 系统上检查谷歌浏览器是否安装
func CheckChrome() (string, bool) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
	if err != nil {
		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
		if err != nil {
			return "", false
		}
		defer key.Close()
	}
	defer key.Close()
	path, _, err := key.GetStringValue("Path")
	//logger.SugaredLogger.Infof("Chrome安装路径：%s", path)
	if err != nil {
		return "", false
	}
	return path + "\\chrome.exe", true
}

// CheckBrowser 在 Windows 系统上检查Edge浏览器是否安装，并返回安装路径
func CheckBrowser() (string, bool) {
	if path, ok := CheckChrome(); ok {
		return path, true
	}

	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
	if err != nil {
		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
		if err != nil {
			return "", false
		}
		defer key.Close()
	}
	defer key.Close()
	path, _, err := key.GetStringValue("Path")
	//logger.SugaredLogger.Infof("Edge安装路径：%s", path)
	if err != nil {
		return "", false
	}
	return path + "\\msedge.exe", true
}

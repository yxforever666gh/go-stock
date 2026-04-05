package main

// @Author spark
// @Date 2025/7/8 18:51
// @Desc
//-----------------------------------------------------------------------------------

import "runtime"

// IsWindows 判断是否为 Windows 系统
func IsWindows() bool {
	return runtime.GOOS == "windows"
}

// IsMacOS 判断是否为 macOS 系统
func IsMacOS() bool {
	return runtime.GOOS == "darwin"
}

// IsLinux 判断是否为 Linux 系统
func IsLinux() bool {
	return runtime.GOOS == "linux"
}

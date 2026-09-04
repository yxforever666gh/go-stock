package main

import (
	"fmt"
	log "go-stock/backend/logger"
	"os"
	"runtime/debug"
	"strings"
)

func checkDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
		log.SugaredLogger.Info("create dir: " + dir)
	}
}

func PanicHandler() {
	if r := recover(); r != nil {
		fmt.Printf("Recovered from panic: %v\n", r)
		debug.PrintStack()
	}
}

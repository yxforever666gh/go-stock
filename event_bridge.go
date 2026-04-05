package main

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var runtimeEventsEnabled atomic.Bool

var webEventHubMu sync.RWMutex
var webEventHub *WebEventHub

func setRuntimeEventsEnabled(enabled bool) {
	runtimeEventsEnabled.Store(enabled)
}

func setWebEventHub(hub *WebEventHub) {
	webEventHubMu.Lock()
	defer webEventHubMu.Unlock()
	webEventHub = hub
}

func emitEvent(ctx context.Context, eventName string, payload any) {
	if runtimeEventsEnabled.Load() && ctx != nil {
		runtime.EventsEmit(ctx, eventName, payload)
	}

	webEventHubMu.RLock()
	hub := webEventHub
	webEventHubMu.RUnlock()
	if hub != nil {
		hub.Emit(eventName, payload)
	}
}

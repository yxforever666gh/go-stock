package main

import (
	"context"
	"sync"
)

var webEventHubMu sync.RWMutex
var webEventHub *WebEventHub

func setWebEventHub(hub *WebEventHub) {
	webEventHubMu.Lock()
	defer webEventHubMu.Unlock()
	webEventHub = hub
}

func emitEvent(ctx context.Context, eventName string, payload any) {
	webEventHubMu.RLock()
	hub := webEventHub
	webEventHubMu.RUnlock()
	if hub != nil {
		hub.Emit(eventName, payload)
	}
}

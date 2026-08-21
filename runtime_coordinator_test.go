package main

import (
	"context"
	"testing"
	"time"
)

func TestRuntimeCoordinatorCancelsAndWaitsForTasks(t *testing.T) {
	coordinator := newRuntimeCoordinator(context.Background())
	started := make(chan struct{})
	finished := make(chan struct{})
	if !coordinator.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}) {
		t.Fatal("expected task to start")
	}
	<-started
	if !coordinator.Shutdown(time.Second) {
		t.Fatal("expected shutdown to wait for task")
	}
	select {
	case <-finished:
	default:
		t.Fatal("task did not observe cancellation")
	}
	if coordinator.Go(func(context.Context) {}) {
		t.Fatal("coordinator accepted a task after shutdown")
	}
}

func TestRuntimeCoordinatorShutdownTimeout(t *testing.T) {
	coordinator := newRuntimeCoordinator(context.Background())
	release := make(chan struct{})
	if !coordinator.Go(func(context.Context) { <-release }) {
		t.Fatal("expected task to start")
	}
	if coordinator.Shutdown(time.Millisecond) {
		t.Fatal("expected shutdown timeout")
	}
	close(release)
	if !coordinator.Shutdown(time.Second) {
		t.Fatal("expected subsequent shutdown to observe completion")
	}
}

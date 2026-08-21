package main

import (
	"context"
	"sync"
	"time"
)

type runtimeCoordinator struct {
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopped  bool
	wait     sync.WaitGroup
	stopOnce sync.Once
}

func newRuntimeCoordinator(parent context.Context) *runtimeCoordinator {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &runtimeCoordinator{ctx: ctx, cancel: cancel}
}

func (coordinator *runtimeCoordinator) Context() context.Context {
	if coordinator == nil || coordinator.ctx == nil {
		return context.Background()
	}
	return coordinator.ctx
}

func (coordinator *runtimeCoordinator) Go(task func(context.Context)) bool {
	if coordinator == nil || task == nil {
		return false
	}
	coordinator.mu.Lock()
	if coordinator.stopped || coordinator.ctx.Err() != nil {
		coordinator.mu.Unlock()
		return false
	}
	coordinator.wait.Add(1)
	coordinator.mu.Unlock()
	go func() {
		defer coordinator.wait.Done()
		defer PanicHandler()
		task(coordinator.Context())
	}()
	return true
}

func (coordinator *runtimeCoordinator) Shutdown(timeout time.Duration) bool {
	if coordinator == nil {
		return true
	}
	coordinator.stopOnce.Do(func() {
		coordinator.mu.Lock()
		coordinator.stopped = true
		coordinator.cancel()
		coordinator.mu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		coordinator.wait.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (a *App) taskContext() context.Context {
	if a != nil && a.runtime != nil {
		return a.runtime.Context()
	}
	if a != nil && a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) goTask(task func(context.Context)) bool {
	if a != nil && a.runtime != nil {
		return a.runtime.Go(task)
	}
	if task == nil {
		return false
	}
	go task(a.taskContext())
	return true
}

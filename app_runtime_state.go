package main

import (
	"errors"
	"fmt"
	"go-stock/internal/releaseinfo"

	"github.com/robfig/cron/v3"
)

func (a *App) recordSchedulerRegistrationError(task, spec string, err error) {
	if a == nil || err == nil {
		return
	}
	failure := fmt.Errorf("register scheduler task %q with %q: %w", task, spec, err)
	a.schedulerErrorsMu.Lock()
	a.schedulerErrors = append(a.schedulerErrors, failure)
	a.schedulerErrorsMu.Unlock()
	releaseinfo.MarkSchedulerReady(false)
	releaseinfo.MarkNotReady(failure)
}

func (a *App) schedulerRegistrationError() error {
	if a == nil {
		return errors.New("application is unavailable")
	}
	a.schedulerErrorsMu.Lock()
	defer a.schedulerErrorsMu.Unlock()
	return errors.Join(a.schedulerErrors...)
}

func (a *App) startSchedulerAfterAssembly() error {
	if err := a.schedulerRegistrationError(); err != nil {
		return err
	}
	if a.cron == nil {
		return errors.New("scheduler is unavailable")
	}
	a.cron.Start()
	return nil
}

func (a *App) setCronEntry(key string, entryID cron.EntryID) {
	a.cronEntrysMu.Lock()
	defer a.cronEntrysMu.Unlock()
	a.cronEntrys[key] = entryID
}

func (a *App) getCronEntry(key string) (cron.EntryID, bool) {
	a.cronEntrysMu.RLock()
	defer a.cronEntrysMu.RUnlock()
	entryID, ok := a.cronEntrys[key]
	return entryID, ok
}

func (a *App) deleteCronEntry(key string) {
	a.cronEntrysMu.Lock()
	defer a.cronEntrysMu.Unlock()
	delete(a.cronEntrys, key)
}

func (a *App) snapshotCronEntries() map[string]cron.EntryID {
	a.cronEntrysMu.RLock()
	defer a.cronEntrysMu.RUnlock()
	entries := make(map[string]cron.EntryID, len(a.cronEntrys))
	for key, entryID := range a.cronEntrys {
		entries[key] = entryID
	}
	return entries
}

func (a *App) tryMarkDomReadyDone() bool {
	a.domReadyMu.Lock()
	defer a.domReadyMu.Unlock()
	if a.domReadyDone {
		return false
	}
	a.domReadyDone = true
	return true
}

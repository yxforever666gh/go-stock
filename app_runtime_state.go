package main

import (
	"go-stock/backend/logger"

	"github.com/robfig/cron/v3"
)

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

func (a *App) tryAcquireSummaryTask() bool {
	a.summaryTaskMu.Lock()
	defer a.summaryTaskMu.Unlock()
	if a.summaryTaskBusy {
		return false
	}
	a.summaryTaskBusy = true
	return true
}

func (a *App) isSummaryTaskBusy() bool {
	a.summaryTaskMu.Lock()
	defer a.summaryTaskMu.Unlock()
	return a.summaryTaskBusy
}

func (a *App) releaseSummaryTask() {
	a.summaryTaskMu.Lock()
	a.summaryTaskBusy = false
	a.summaryTaskMu.Unlock()
}

func (a *App) tryAcquireYieldEmailTask() bool {
	a.yieldEmailTaskMu.Lock()
	defer a.yieldEmailTaskMu.Unlock()
	if a.yieldEmailTaskBusy {
		return false
	}
	a.yieldEmailTaskBusy = true
	return true
}

func (a *App) isYieldEmailTaskBusy() bool {
	a.yieldEmailTaskMu.Lock()
	defer a.yieldEmailTaskMu.Unlock()
	return a.yieldEmailTaskBusy
}

func (a *App) releaseYieldEmailTask() {
	a.yieldEmailTaskMu.Lock()
	a.yieldEmailTaskBusy = false
	a.yieldEmailTaskMu.Unlock()
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

func (a *App) withYieldEmailTaskLock(taskName string, fn func() string) string {
	if !a.tryAcquireYieldEmailTask() {
		logger.SugaredLogger.Warnf("跳过邮件发送任务: task=%s reason=上一次邮件任务仍在执行", taskName)
		return "发送失败: 上一次邮件任务仍在执行"
	}
	defer a.releaseYieldEmailTask()

	logger.SugaredLogger.Infof("开始执行邮件发送任务: task=%s", taskName)
	return fn()
}

package data

import (
	"sync"
	"time"

	"go-stock/backend/logger"
)

var throttledLogMu sync.Mutex
var throttledLogLast = map[string]time.Time{}

// logErrorEvery limits duplicate error logs by key within an interval.
func logErrorEvery(key string, interval time.Duration, format string, args ...any) {
	now := time.Now()

	throttledLogMu.Lock()
	last, ok := throttledLogLast[key]
	if ok && now.Sub(last) < interval {
		throttledLogMu.Unlock()
		return
	}
	throttledLogLast[key] = now
	throttledLogMu.Unlock()

	logger.SugaredLogger.Errorf(format, args...)
}

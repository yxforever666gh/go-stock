// Package sqlitedb contains SQLite-specific behavior shared across domain
// repositories without owning their transactions or logging policy.
package sqlitedb

import (
	"context"
	"errors"
	"strings"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

var busyRetryDelays = [...]time.Duration{
	20 * time.Millisecond,
	40 * time.Millisecond,
	80 * time.Millisecond,
	160 * time.Millisecond,
	320 * time.Millisecond,
}

// IsBusy reports whether err represents a retryable SQLite writer-lock error.
// It recognizes modernc extended result codes and the messages emitted when a
// driver or wrapper does not preserve the typed error.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr interface{ Code() int }
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code()
		return code&0xff == sqlite3.SQLITE_BUSY || code&0xff == sqlite3.SQLITE_LOCKED
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "sqlite_locked") ||
		strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "(517)")
}

// Retry executes operation once and retries SQLite busy errors after fixed
// 20, 40, 80, 160, and 320 millisecond delays. onRetry runs before each wait;
// callers may use it for domain-specific logging without duplicating the loop.
func Retry(ctx context.Context, operation func() error, onRetry func(retry int, delay time.Duration, err error)) error {
	for attempt := 0; ; attempt++ {
		err := operation()
		if !IsBusy(err) || attempt == len(busyRetryDelays) {
			return err
		}
		delay := busyRetryDelays[attempt]
		if onRetry != nil {
			onRetry(attempt+1, delay, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

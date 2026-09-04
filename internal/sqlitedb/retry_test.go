package sqlitedb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type codedError struct{ code int }

func (err *codedError) Error() string { return fmt.Sprintf("sqlite code %d", err.code) }
func (err *codedError) Code() int     { return err.code }

func TestIsBusyRecognizesExtendedCodesAndMessages(t *testing.T) {
	for _, code := range []int{5, 6, 261, 262, 517, 518, 773} {
		if !IsBusy(fmt.Errorf("wrapped: %w", &codedError{code: code})) {
			t.Errorf("code %d was not recognized as retryable", code)
		}
	}
	for _, err := range []error{
		errors.New("SQLITE_BUSY_SNAPSHOT"),
		errors.New("SQLITE_LOCKED"),
		errors.New("database is locked (5)"),
		errors.New("database table is locked"),
		errors.New("database is locked (517)"),
	} {
		if !IsBusy(err) {
			t.Errorf("error %q was not recognized as retryable", err)
		}
	}
	if IsBusy(errors.New("unique constraint failed")) {
		t.Fatal("non-locking error must not be retried")
	}
}

func TestRetryUsesAllFixedBackoffs(t *testing.T) {
	attempts := 0
	observed := make([]time.Duration, 0, len(busyRetryDelays))
	err := Retry(context.Background(), func() error {
		attempts++
		if attempts <= len(busyRetryDelays) {
			return &codedError{code: 517}
		}
		return nil
	}, func(_ int, delay time.Duration, _ error) {
		observed = append(observed, delay)
	})
	if err != nil || attempts != len(busyRetryDelays)+1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
	want := []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("delays=%v want=%v", observed, want)
	}
}

func TestRetryStopsWaitingWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := Retry(ctx, func() error {
		attempts++
		return &codedError{code: 5}
	}, func(_ int, _ time.Duration, _ error) {
		cancel()
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

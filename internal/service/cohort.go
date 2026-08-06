package service

import (
	"errors"
	"fmt"
	"strings"
)

type StrategyCohort string

const (
	StrategyCohortCurrent StrategyCohort = "current"
	StrategyCohortLegacy  StrategyCohort = "legacy"
)

var ErrInvalidStrategyCohort = errors.New("invalid strategy cohort")

func requireStrategyCohort(got, want StrategyCohort) error {
	normalized := StrategyCohort(strings.ToLower(strings.TrimSpace(string(got))))
	if normalized == "" {
		return fmt.Errorf("%w: cohort is required", ErrInvalidStrategyCohort)
	}
	if normalized == "all" {
		return fmt.Errorf("%w: mixed current and legacy reads are forbidden", ErrInvalidStrategyCohort)
	}
	if normalized != want {
		return fmt.Errorf("%w: %q is not valid for the %s read model", ErrInvalidStrategyCohort, got, want)
	}
	return nil
}

package main

import (
	"errors"
	"reflect"
	"testing"
)

func callForRPC(t *testing.T, fn any) (any, error) {
	t.Helper()
	value := reflect.ValueOf(fn)
	return normalizeMethodResults(value.Type(), value.Call(nil))
}

func TestNormalizeMethodResultsUnwrapsNilError(t *testing.T) {
	result, err := callForRPC(t, func() (string, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %#v, want %q", result, "ok")
	}
}

func TestNormalizeMethodResultsReturnsNonNilError(t *testing.T) {
	want := errors.New("boom")
	result, err := callForRPC(t, func() (string, error) { return "ignored", want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil", result)
	}
}

func TestNormalizeMethodResultsPreservesNonErrorTuple(t *testing.T) {
	result, err := callForRPC(t, func() (string, int) { return "ok", 2 })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tuple, ok := result.([]any)
	if !ok || len(tuple) != 2 || tuple[0] != "ok" || tuple[1] != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestStartAIAnalysisRPCIsAllowed(t *testing.T) {
	if !isRPCMethodAllowed("StartAIAnalysis") {
		t.Fatal("StartAIAnalysis must be available to the research-center button")
	}
}

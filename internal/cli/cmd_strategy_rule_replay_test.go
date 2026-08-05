package cli

import (
	"bytes"
	"testing"
	"time"
)

func TestParseOptionalReplayDates(t *testing.T) {
	from, to, err := parseOptionalReplayDates("2026-04-01", "2026-08-04", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if from.Format(time.DateOnly) != "2026-04-01" || to.Format(time.DateOnly) != "2026-08-04" {
		t.Fatalf("unexpected replay dates: %s %s", from, to)
	}
	if _, _, err := parseOptionalReplayDates("2026-08-05", "2026-08-04", time.UTC); err == nil {
		t.Fatal("expected reversed replay dates to fail")
	}
}

func TestStrategyRuleReplayAcceptsGlobalReadOnlyDatabasePath(t *testing.T) {
	opts, command, args, err := parseRootArgs([]string{
		"--db-path", `H:\cache\frozen-copy.db`,
		"strategy-rule-replay", "--expect-count", "226",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.DBPath != `H:\cache\frozen-copy.db` || command != "strategy-rule-replay" || len(args) != 2 {
		t.Fatalf("unexpected routing: opts=%+v command=%q args=%v", opts, command, args)
	}
}

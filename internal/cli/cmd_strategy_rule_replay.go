package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
)

func runStrategyRuleReplay(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("strategy-rule-replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var fromText, toText string
	var expectedCount int
	var jsonOut bool
	fs.StringVar(&fromText, "from", "", "optional recommendation date lower bound (YYYY-MM-DD)")
	fs.StringVar(&toText, "to", "", "optional recommendation date upper bound (YYYY-MM-DD, inclusive)")
	fs.IntVar(&expectedCount, "expect-count", 0, "fail when the selected legacy-rule corpus count differs")
	fs.BoolVar(&jsonOut, "json", g.JSON, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("strategy-rule-replay does not accept positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	from, to, err := parseOptionalReplayDates(fromText, toText, time.Local)
	if err != nil {
		return err
	}
	report, err := data.ReplayLegacyStructuredRulesCacheOnly(context.Background(), db.Dao, data.LegacyStructuredRuleReplayOptions{
		From: from, To: to, ExpectedRuleCount: expectedCount,
	})
	if err != nil {
		return err
	}
	if !report.Deterministic || report.DeterminismViolations != 0 {
		return errors.New("legacy structured-rule replay is not deterministic")
	}
	if report.InvalidRules != 0 || report.CausalityViolations != 0 || report.TPlusOneViolations != 0 {
		return fmt.Errorf("legacy structured-rule replay invariant failure: invalid=%d causality=%d t+1=%d", report.InvalidRules, report.CausalityViolations, report.TPlusOneViolations)
	}
	if jsonOut {
		body, err := marshalPrettyJSON(report)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}
	_, _ = fmt.Fprintln(stdout, "Legacy structured-rule execution replay complete (cache-only, profitability proof: false).")
	_, _ = fmt.Fprintf(stdout, "  rules=%d parsed=%d cache_available=%d cache_missing=%d missing_exit_plan=%d activated=%d closed=%d\n", report.TotalRules, report.ParsedRules, report.CacheAvailableRules, report.CacheMissingRules, report.MissingExitPlanRules, report.ActivatedRules, report.ClosedRules)
	_, _ = fmt.Fprintf(stdout, "  invariant_failures: invalid=%d causality=%d t+1=%d deterministic=%t\n", report.InvalidRules, report.CausalityViolations, report.TPlusOneViolations, report.Deterministic)
	_, _ = fmt.Fprintf(stdout, "  hash=%s repeat_hash=%s\n", report.ResultHash, report.RepeatedResultHash)
	return nil
}

func parseOptionalReplayDates(fromText, toText string, loc *time.Location) (time.Time, time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	parse := func(name, raw string) (time.Time, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return time.Time{}, nil
		}
		value, err := time.ParseInLocation(time.DateOnly, raw, loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must use YYYY-MM-DD: %w", name, err)
		}
		return value, nil
	}
	from, err := parse("--from", fromText)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parse("--to", toText)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return time.Time{}, time.Time{}, errors.New("--to cannot precede --from")
	}
	return from, to, nil
}

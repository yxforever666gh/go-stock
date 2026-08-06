package cli

import (
	"errors"
	"flag"
	"io"
)

var errHistoricalMarketSummaryRepairDisabled = errors.New("historical market summary repair is disabled; legacy recommendations are read-only")

func runRepairMarketSummary(args []string, g GlobalOptions, _ io.Writer, _ io.Writer) error {
	fs := flag.NewFlagSet("repair-market-summary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", g.JSON, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("repair-market-summary does not accept positional arguments")
	}

	return errHistoricalMarketSummaryRepairDisabled
}

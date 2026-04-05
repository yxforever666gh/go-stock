package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"go-stock/backend/data"
)

func runQuote(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	_ = stderr
	fs := flag.NewFlagSet("quote", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		code    string
		jsonOut bool
	)
	fs.StringVar(&code, "code", "", "股票代码，如 sh600519/sz000001/hk00700/gb_aapl")
	fs.BoolVar(&jsonOut, "json", g.JSON, "JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("请通过 --code 提供股票代码")
	}

	items, err := data.NewStockDataApi().GetStockCodeRealTimeData(code)
	if err != nil {
		return err
	}
	if items == nil || len(*items) == 0 {
		return fmt.Errorf("未查询到股票: %s", code)
	}
	item := (*items)[0]

	if jsonOut {
		body, err := marshalPrettyJSON(item)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}
	_, _ = fmt.Fprintln(stdout, formatQuoteText(&item))
	return nil
}

package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"go-stock/backend/data"
)

func runSearch(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	_ = stderr
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		words       string
		pageSize    int
		fingerprint string
		jsonOut     bool
	)
	fs.StringVar(&words, "words", "", "选股自然语言条件")
	fs.IntVar(&pageSize, "page-size", 5000, "返回条数")
	fs.StringVar(&fingerprint, "qgqp-b-id", "", "东财 qgqp_b_id")
	fs.BoolVar(&jsonOut, "json", g.JSON, "JSON 输出")
	if err := fs.Parse(args); err != nil {
		return err
	}

	words = strings.TrimSpace(words)
	if words == "" {
		return errors.New("请通过 --words 提供选股条件")
	}
	if pageSize <= 0 {
		pageSize = 5000
	}

	resolvedFingerprint, err := ResolveFingerprint(strings.TrimSpace(fingerprint))
	if err != nil {
		return err
	}

	res := data.NewSearchStockApiWithFingerprint(words, resolvedFingerprint).SearchStock(pageSize)
	if jsonOut {
		body, err := marshalPrettyJSON(res)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
	} else {
		_, _ = fmt.Fprintln(stdout, formatSearchText(res))
	}
	if asInt(res["code"], 0) < 0 {
		return fmt.Errorf("%v", res["message"])
	}
	return nil
}

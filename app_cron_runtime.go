package main

import (
	"fmt"
	"strings"
)

func buildSummaryCronSpec(hhmm string) string {
	parts := strings.Split(hhmm, ":")
	if len(parts) != 2 {
		return ""
	}
	return fmt.Sprintf("CRON_TZ=Asia/Shanghai 0 %s %s * * 1-5", parts[1], parts[0])
}

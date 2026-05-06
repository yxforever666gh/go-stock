package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"
)

type marketSummaryRecommendBackfillRow struct {
	ID           uint   `json:"id"`
	CreatedAt    string `json:"createdAt"`
	ProviderName string `json:"providerName"`
	ModelName    string `json:"modelName"`
	Saved        int    `json:"saved"`
	Error        string `json:"error,omitempty"`
}

func runBackfillMarketSummaryRecommend(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backfill-market-summary-recommend", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var dateText string
	var startText string
	var endText string
	var jsonOut bool
	var dryRun bool
	fs.StringVar(&dateText, "date", "", "按交易日补写，格式 YYYY-MM-DD")
	fs.StringVar(&startText, "start", "", "开始时间，格式 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS")
	fs.StringVar(&endText, "end", "", "结束时间，格式 YYYY-MM-DD 或 YYYY-MM-DD HH:MM:SS")
	fs.BoolVar(&jsonOut, "json", g.JSON, "输出 JSON")
	fs.BoolVar(&dryRun, "dry-run", false, "只统计历史报告，不写入推荐记录")
	if err := fs.Parse(args); err != nil {
		return err
	}

	loc := time.Local
	start, end, err := resolveBackfillMarketSummaryRecommendWindow(dateText, startText, endText, loc)
	if err != nil {
		return err
	}

	reports := make([]models.AIResponseResult, 0, 8)
	if err := db.Dao.Model(&models.AIResponseResult{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Where("(stock_code = ? OR stock_name = ?)", "市场资讯", "市场资讯").
		Order("created_at ASC").
		Find(&reports).Error; err != nil {
		return err
	}

	rows := make([]marketSummaryRecommendBackfillRow, 0, len(reports))
	totalSaved := 0
	for _, report := range reports {
		row := marketSummaryRecommendBackfillRow{
			ID:           report.ID,
			CreatedAt:    report.CreatedAt.In(loc).Format(time.DateTime),
			ProviderName: strings.TrimSpace(report.ProviderName),
			ModelName:    strings.TrimSpace(report.ModelName),
		}
		if !dryRun {
			saved, saveErr := data.EnsureMarketSummaryRecommendStocksSaved(report.Content, report.ProviderName, report.ModelName, report.CreatedAt)
			row.Saved = saved
			totalSaved += saved
			if saveErr != nil {
				row.Error = saveErr.Error()
			}
		}
		rows = append(rows, row)
	}

	if jsonOut {
		body, err := marshalPrettyJSON(map[string]any{
			"databasePath": g.DBPath,
			"commandName":  "backfill-market-summary-recommend",
			"dryRun":       dryRun,
			"start":        start.In(loc).Format(time.DateTime),
			"end":          end.In(loc).Format(time.DateTime),
			"reports":      len(reports),
			"saved":        totalSaved,
			"rows":         rows,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}

	if dryRun {
		_, _ = fmt.Fprintf(stdout, "市场资讯推荐补写预检完成：报告 %d 条，未写库\n", len(reports))
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "市场资讯推荐补写完成：报告 %d 条，新增推荐 %d 条\n", len(reports), totalSaved)
	for _, row := range rows {
		if row.Error != "" {
			_, _ = fmt.Fprintf(stdout, "  - #%d %s 新增 %d 条，错误：%s\n", row.ID, row.CreatedAt, row.Saved, row.Error)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "  - #%d %s 新增 %d 条\n", row.ID, row.CreatedAt, row.Saved)
	}
	return nil
}

func resolveBackfillMarketSummaryRecommendWindow(dateText, startText, endText string, loc *time.Location) (time.Time, time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	dateText = strings.TrimSpace(dateText)
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)
	if dateText != "" {
		if startText != "" || endText != "" {
			return time.Time{}, time.Time{}, errors.New("--date 不能和 --start/--end 同时使用")
		}
		day, err := time.ParseInLocation(time.DateOnly, dateText, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--date 格式错误: %w", err)
		}
		return day, day.Add(24 * time.Hour), nil
	}
	if startText == "" || endText == "" {
		return time.Time{}, time.Time{}, errors.New("必须提供 --date，或同时提供 --start 与 --end")
	}
	start, err := parseBackfillMarketSummaryRecommendTime(startText, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--start 格式错误: %w", err)
	}
	end, err := parseBackfillMarketSummaryRecommendTime(endText, loc)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--end 格式错误: %w", err)
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("--end 必须晚于 --start")
	}
	return start, end, nil
}

func parseBackfillMarketSummaryRecommendTime(text string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	text = strings.TrimSpace(text)
	if t, err := time.ParseInLocation(time.DateTime, text, loc); err == nil {
		return t, nil
	}
	return time.ParseInLocation(time.DateOnly, text, loc)
}

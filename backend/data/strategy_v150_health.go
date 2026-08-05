package data

import (
	"encoding/json"
	"fmt"
	"strings"

	"go-stock/backend/db"
	"go-stock/backend/models"
)

// loadV150ImmutableRunHealthWarnings exposes warnings from the frozen
// production run envelope. Mutable yield projections are deliberately not a
// source here: news, benchmark, quote/model degradation and causal rejection
// must remain attributable to the exact strategy run that observed them.
func loadV150ImmutableRunHealthWarnings(limit int) ([]string, error) {
	if limit <= 0 {
		limit = 30
	}
	if db.Dao == nil || !db.Dao.Migrator().HasTable(&models.StrategyRunSnapshot{}) {
		return []string{}, nil
	}
	rows := make([]models.StrategyRunSnapshot, 0, limit)
	if err := db.Dao.Model(&models.StrategyRunSnapshot{}).
		Where("strategy_version = ? AND mode = ? AND frozen_at IS NOT NULL", marketSummaryVersion150, "production").
		Order("decision_at DESC, run_id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, row := range rows {
		var envelope struct {
			Run struct {
				Warnings []string `json:"warnings"`
			} `json:"run"`
		}
		if err := json.Unmarshal([]byte(row.PayloadJSON), &envelope); err != nil {
			warning := "immutable_run_payload_invalid:" + strings.TrimSpace(row.RunID)
			if _, exists := seen[warning]; !exists {
				seen[warning] = struct{}{}
				result = append(result, formatV150ImmutableRunHealthWarning(row, warning))
			}
			continue
		}
		for _, raw := range envelope.Run.Warnings {
			warning := strings.TrimSpace(raw)
			if warning == "" {
				continue
			}
			if _, exists := seen[warning]; exists {
				continue
			}
			seen[warning] = struct{}{}
			result = append(result, formatV150ImmutableRunHealthWarning(row, warning))
			if len(result) == limit {
				return result, nil
			}
		}
	}
	return result, nil
}

func formatV150ImmutableRunHealthWarning(row models.StrategyRunSnapshot, warning string) string {
	context := strings.TrimSpace(row.TradeDate)
	if slot := strings.TrimSpace(row.RunSlot); slot != "" {
		context = strings.TrimSpace(context + " " + slot)
	}
	if context == "" {
		context = strings.TrimSpace(row.RunID)
	}
	return fmt.Sprintf("[%s] %s", context, strings.TrimSpace(warning))
}

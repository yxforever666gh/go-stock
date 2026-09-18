package research2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-stock/internal/trading"

	"gorm.io/gorm"
)

const DefaultSlot = "09:50"

// Slots are account identities, not task identities. A late task can publish to
// a different account without moving either account's scheduled liquidation.
func Slots() []string {
	result := make([]string, 24)
	for i := range result {
		minute := 9*60 + 30 + 5*i
		result[i] = fmt.Sprintf("%02d:%02d", minute/60, minute%60)
	}
	return result
}

func ValidSlot(slot string) bool {
	for _, item := range Slots() {
		if item == slot {
			return true
		}
	}
	return false
}

func SlotAt(at time.Time) string {
	local := at.In(shanghai())
	minute := local.Hour()*60 + local.Minute()
	if minute < 570 || minute >= 690 {
		return ""
	}
	minute -= minute % 5
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

func SlotTime(day time.Time, slot string) time.Time {
	local := day.In(shanghai())
	var hour, minute int
	_, _ = fmt.Sscanf(normalizeSlot(slot), "%d:%d", &hour, &minute)
	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, shanghai())
}

func normalizeSlot(slot string) string {
	if slot == "" {
		return DefaultSlot
	}
	return slot
}

// OpeningMinuteMinimum relaxes only the first five minutes after the open.
// No data from an unclosed minute or previous session is synthesized.
func OpeningMinuteMinimum(cutoff time.Time) int {
	at := cutoff.In(shanghai())
	open := SlotTime(at, "09:30")
	if !at.Before(open) && at.Before(open.Add(5*time.Minute)) {
		return 0
	}
	return 4
}

func (r *Repository) WithSlot(slot string) *Repository {
	copy := *r
	copy.slot = normalizeSlot(slot)
	return &copy
}
func (r *Repository) accountSlot() string { return normalizeSlot(r.slot) }
func (r *Repository) accountQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Where("slot = ?", r.accountSlot())
}
func (r *Repository) nowTime() time.Time {
	if r.now != nil {
		return r.now().In(shanghai())
	}
	return time.Now().In(shanghai())
}

type SlotStatus struct {
	Slot            string     `json:"slot"`
	Label           string     `json:"label"`
	TradingDate     string     `json:"tradingDate"`
	WinnerRunID     string     `json:"winnerRunId"`
	SellCompletedAt *time.Time `json:"sellCompletedAt,omitempty"`
	Status          string     `json:"status"`
}

func (r *Repository) SlotStatuses(ctx context.Context, at time.Time) ([]SlotStatus, error) {
	date := at.In(shanghai()).Format("2006-01-02")
	var chains []ExecutionChain
	if err := r.db.WithContext(ctx).Where("trading_date = ?", date).Find(&chains).Error; err != nil {
		return nil, err
	}
	bySlot := map[string]ExecutionChain{}
	for _, chain := range chains {
		bySlot[chain.Slot] = chain
	}
	result := make([]SlotStatus, 0, 24)
	for _, slot := range Slots() {
		chain, exists := bySlot[slot]
		state := "pending"
		if exists {
			state = chain.Status
		}
		result = append(result, SlotStatus{Slot: slot, Label: slot + "–" + SlotTime(at, slot).Add(5*time.Minute).Format("15:04"), TradingDate: date, WinnerRunID: chain.WinnerRunID, SellCompletedAt: chain.SellCompletedAt, Status: state})
	}
	return result, nil
}

// DailyEmailRun is synthetic: one stable delivery key per trading day, without
// manufacturing an analysis run or occupying any result slot.
func (r *Repository) DailyEmailRun(ctx context.Context, at time.Time) (AnalysisRun, error) {
	states, err := r.SlotStatuses(ctx, at)
	if err != nil {
		return AnalysisRun{}, err
	}
	var body strings.Builder
	body.WriteString("# 研究中心2五分钟分区汇总\n\n")
	for _, state := range states {
		body.WriteString(fmt.Sprintf("## %s\n\n- 定时卖出完成：%t\n", state.Label, state.SellCompletedAt != nil))
		if state.WinnerRunID == "" {
			body.WriteString("- 尚无有效推荐报告\n\n")
			continue
		}
		run, err := r.GetRun(ctx, state.WinnerRunID)
		if err != nil {
			return AnalysisRun{}, err
		}
		body.WriteString(run.ReportMarkdown + "\n\n")
	}
	return AnalysisRun{RunID: "research2-daily-" + at.In(shanghai()).Format("2006-01-02"), TradingDate: at.In(shanghai()).Format("2006-01-02"), Status: "success", GeneratedAt: &at, ReportMarkdown: body.String()}, nil
}

func validPrice(value float64) bool { return value > 0 && value < 1e100 }

// FinalizeRun takes the SQLite writer lock before sampling the publication
// clock. A write retry resamples it; a rollback neither claims nor publishes.
func (r *Repository) finalizeSlotRun(ctx context.Context, run *AnalysisRun, items []Recommendation, render func() string) error {
	return research2TransactionWithWriteRetry(ctx, r.db, func(tx *gorm.DB) error {
		if err := lockResearch2AccountForWrite(tx); err != nil {
			return err
		}
		var stored AnalysisRun
		if err := tx.Where("run_id = ?", run.RunID).First(&stored).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if stored.PersistedAt != nil {
			*run = stored
			return nil
		}
		persisted := r.nowTime()
		slot := SlotAt(persisted)
		run.OnTime = SlotAt(persisted) == run.ScheduledSlot
		run.PersistedAt = &persisted
		run.GeneratedAt = &persisted
		run.Slot = slot
		run.Published = false
		run.ChainID = ""
		run.ArchiveReason = ""
		if run.TriggerSource == "diagnostic" {
			run.ArchiveReason = "链路诊断，仅保留报告，不发布推荐或交易"
		} else if slot == "" || persisted.Format("2006-01-02") != run.TradingDate {
			run.ArchiveReason = "上午窗口外完成，仅保留报告"
		} else {
			chain, err := ensureExecutionChain(tx, slot, run.TradingDate, SlotTime(persisted, slot), persisted)
			if err != nil {
				return err
			}
			permissionErr := r.checkNewPositionsAllowed(ctx, tx)
			if permissionErr != nil && !errors.Is(permissionErr, trading.ErrNewPositionsDisabled) {
				return permissionErr
			}
			if stored.ArchiveReason != "" {
				run.ArchiveReason = stored.ArchiveReason
			} else if chain.WinnerRunID != "" {
				run.ArchiveReason = "本区间已有先落盘报告，仅保留报告"
			} else if permissionErr != nil {
				run.ArchiveReason = "自动研究已关闭，仅保留报告"
			} else {
				result := tx.Model(&ExecutionChain{}).Where("chain_id = ? AND winner_run_id = ?", chain.ChainID, "").Updates(map[string]any{"winner_run_id": run.RunID, "latest_run_id": run.RunID, "root_run_id": run.RunID, "status": "running", "target_slots": DailyTargetSlots})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("slot publication claim changed")
				}
				run.Published = true
				run.ChainID = chain.ChainID
				run.RequestedSlots = DailyTargetSlots
			}
		}
		for i := range items {
			items[i].Slot = slot
			items[i].SignalAt = persisted
			items[i].TargetBuyAt = persisted
			items[i].Status = "buy_pending"
			if !run.Published {
				items[i].Status = "analysis_only"
				items[i].FailureReason = run.ArchiveReason
			}
		}
		run.ReportMarkdown = render()
		if run.ArchiveReason != "" {
			run.ReportMarkdown += "\n\n> " + run.ArchiveReason
		}
		if err := tx.Save(run).Error; err != nil {
			return err
		}
		if !run.Published || len(items) == 0 {
			return nil
		}
		rows := append([]Recommendation(nil), items...)
		for i := range rows {
			rows[i].ID = 0
		}
		return tx.Create(&rows).Error
	})
}

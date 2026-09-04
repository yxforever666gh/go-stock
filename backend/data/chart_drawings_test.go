package data

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-stock/backend/instruments"
)

func TestChartDrawingRevisionTombstoneSoftDeleteAndRestore(t *testing.T) {
	initDatabaseForTest(t, filepath.Join(t.TempDir(), "chart-drawings.db"), testSchemaChartDrawing)
	now := time.Date(2026, 8, 28, 15, 0, 0, 0, cnLocation())
	instrument, _ := instruments.ParseInstrumentID("159915", "etf", "SZ")
	scope := ChartDrawingScope{ScopeType: "user", ScopeID: "local", Request: ChartRequest{Instrument: instrument, Period: ChartPeriod5Minute, Adjustment: ChartAdjustmentNone}}
	service := NewChartService()
	service.now = func() time.Time { return now }
	created := now.Add(-time.Hour)
	drawing := ChartDrawing{ID: "trend-1", Type: "trend_line", Points: []ChartDrawingPoint{{Time: now.Add(-10 * time.Minute), Value: 1}, {Time: now, Value: 2}}, CreatedAt: &created}
	document, err := service.PutDrawings(context.Background(), scope, 0, []ChartDrawing{drawing})
	if err != nil || document.Revision != 1 || len(document.Drawings) != 1 {
		t.Fatalf("create document=%+v err=%v", document, err)
	}
	deletedAt := now.Add(time.Minute)
	drawing.DeletedAt = &deletedAt
	drawing.UpdatedAt = &deletedAt
	document, err = service.PutDrawings(context.Background(), scope, 1, []ChartDrawing{drawing})
	if err != nil || document.Revision != 2 || document.Drawings[0].DeletedAt == nil || !document.Drawings[0].DeletedAt.Equal(deletedAt) || document.Drawings[0].CreatedAt == nil || !document.Drawings[0].CreatedAt.Equal(created) {
		t.Fatalf("drawing tombstone did not survive: document=%+v err=%v", document, err)
	}
	if _, err := service.PutDrawings(context.Background(), scope, 1, []ChartDrawing{drawing}); !errors.Is(err, ErrDrawingRevisionConflict) {
		t.Fatalf("stale revision error=%v", err)
	}
	var revisionCount int64
	if err := service.mainDB.Model(&chartDrawingRevisionRow{}).Count(&revisionCount).Error; err != nil || revisionCount != 2 {
		t.Fatalf("revision history count=%d err=%v", revisionCount, err)
	}
	var tombstoneRevision chartDrawingRevisionRow
	if err := service.mainDB.Where("revision = ?", 2).First(&tombstoneRevision).Error; err != nil || !strings.Contains(tombstoneRevision.DrawingsJSON, "deletedAt") {
		t.Fatalf("tombstone revision=%+v err=%v", tombstoneRevision, err)
	}
	document, err = service.DeleteDrawings(context.Background(), scope, 2)
	if err != nil || document.Revision != 3 || document.DeletedAt == nil || len(document.Drawings) != 0 {
		t.Fatalf("soft delete document=%+v err=%v", document, err)
	}
	drawing.DeletedAt = nil
	document, err = service.PutDrawings(context.Background(), scope, 3, []ChartDrawing{drawing})
	if err != nil || document.Revision != 4 || document.DeletedAt != nil || len(document.Drawings) != 1 {
		t.Fatalf("restore document=%+v err=%v", document, err)
	}
}

package data

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrDrawingRevisionConflict = errors.New("chart drawing revision conflict")
	ErrDrawingNotFound         = errors.New("chart drawing document not found")
)

const (
	defaultDrawingScopeType = "user"
	defaultDrawingScopeID   = "local"
	maxChartDrawings        = 500
	maxChartDrawingJSON     = 256 * 1024
)

type chartDrawingDocumentRow struct {
	ID                int64      `gorm:"column:id;primaryKey"`
	DrawingDocumentID string     `gorm:"column:drawing_document_id"`
	ScopeType         string     `gorm:"column:scope_type"`
	ScopeID           string     `gorm:"column:scope_id"`
	AssetType         string     `gorm:"column:asset_type"`
	Market            string     `gorm:"column:market"`
	Code              string     `gorm:"column:code"`
	Period            string     `gorm:"column:period"`
	Adjustment        string     `gorm:"column:adjustment"`
	Revision          int64      `gorm:"column:revision"`
	DrawingsJSON      string     `gorm:"column:drawings_json"`
	DeletedAt         *time.Time `gorm:"column:deleted_at"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

func (chartDrawingDocumentRow) TableName() string { return "chart_drawing_documents" }

type chartDrawingRevisionRow struct {
	ID           int64      `gorm:"column:id;primaryKey"`
	DocumentID   string     `gorm:"column:document_id"`
	Revision     int64      `gorm:"column:revision"`
	DrawingsJSON string     `gorm:"column:drawings_json"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
}

func (chartDrawingRevisionRow) TableName() string { return "chart_drawing_revisions" }

func (s *ChartService) GetDrawings(ctx context.Context, scope ChartDrawingScope) (ChartDrawingDocument, error) {
	scope, err := normalizeDrawingScope(scope, s.now())
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	if s.mainDB == nil {
		return ChartDrawingDocument{}, errors.New("main database is unavailable")
	}
	row := chartDrawingDocumentRow{}
	err = drawingScopeQuery(s.mainDB.WithContext(ctx), scope).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return emptyDrawingDocument(scope), nil
	}
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	return drawingDocumentFromRow(scope, row)
}

func (s *ChartService) PutDrawings(ctx context.Context, scope ChartDrawingScope, expectedRevision int64, drawings []ChartDrawing) (ChartDrawingDocument, error) {
	scope, err := normalizeDrawingScope(scope, s.now())
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	if expectedRevision < 0 {
		return ChartDrawingDocument{}, errors.New("expectedRevision must be non-negative")
	}
	payload, err := validateAndMarshalDrawings(drawings)
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	if s.mainDB == nil {
		return ChartDrawingDocument{}, errors.New("main database is unavailable")
	}
	now := chartShanghaiTime(s.now())
	err = s.mainDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := chartDrawingDocumentRow{}
		findErr := drawingScopeQuery(tx, scope).First(&row).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			if expectedRevision != 0 {
				return ErrDrawingRevisionConflict
			}
			documentID, idErr := newDrawingDocumentID()
			if idErr != nil {
				return idErr
			}
			row = chartDrawingDocumentRow{DrawingDocumentID: documentID, ScopeType: scope.ScopeType, ScopeID: scope.ScopeID,
				AssetType: scope.Request.Instrument.AssetType, Market: scope.Request.Instrument.Market, Code: scope.Request.Instrument.Code,
				Period: scope.Request.Period, Adjustment: scope.Request.Adjustment, Revision: 1, DrawingsJSON: string(payload), CreatedAt: now, UpdatedAt: now}
			if createErr := tx.Create(&row).Error; createErr != nil {
				if isSQLiteUniqueConstraint(createErr) {
					return ErrDrawingRevisionConflict
				}
				return createErr
			}
			return tx.Create(&chartDrawingRevisionRow{DocumentID: documentID, Revision: 1, DrawingsJSON: string(payload), CreatedAt: now}).Error
		}
		if findErr != nil {
			return findErr
		}
		if row.Revision != expectedRevision {
			return ErrDrawingRevisionConflict
		}
		next := expectedRevision + 1
		result := tx.Model(&chartDrawingDocumentRow{}).Where("id = ? AND revision = ?", row.ID, expectedRevision).
			Updates(map[string]any{"revision": next, "drawings_json": string(payload), "deleted_at": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrDrawingRevisionConflict
		}
		return tx.Create(&chartDrawingRevisionRow{DocumentID: row.DrawingDocumentID, Revision: next, DrawingsJSON: string(payload), CreatedAt: now}).Error
	})
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	return s.GetDrawings(ctx, scope)
}

func (s *ChartService) DeleteDrawings(ctx context.Context, scope ChartDrawingScope, expectedRevision int64) (ChartDrawingDocument, error) {
	scope, err := normalizeDrawingScope(scope, s.now())
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	if expectedRevision < 0 {
		return ChartDrawingDocument{}, errors.New("expectedRevision must be non-negative")
	}
	if s.mainDB == nil {
		return ChartDrawingDocument{}, errors.New("main database is unavailable")
	}
	now := chartShanghaiTime(s.now())
	err = s.mainDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := chartDrawingDocumentRow{}
		findErr := drawingScopeQuery(tx, scope).First(&row).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return ErrDrawingNotFound
		}
		if findErr != nil {
			return findErr
		}
		if row.Revision != expectedRevision {
			return ErrDrawingRevisionConflict
		}
		next := expectedRevision + 1
		result := tx.Model(&chartDrawingDocumentRow{}).Where("id = ? AND revision = ?", row.ID, expectedRevision).
			Updates(map[string]any{"revision": next, "deleted_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrDrawingRevisionConflict
		}
		return tx.Create(&chartDrawingRevisionRow{DocumentID: row.DrawingDocumentID, Revision: next, DrawingsJSON: row.DrawingsJSON, DeletedAt: &now, CreatedAt: now}).Error
	})
	if err != nil {
		return ChartDrawingDocument{}, err
	}
	return s.GetDrawings(ctx, scope)
}

func normalizeDrawingScope(scope ChartDrawingScope, now time.Time) (ChartDrawingScope, error) {
	scope.ScopeType = strings.ToLower(strings.TrimSpace(scope.ScopeType))
	if scope.ScopeType == "" {
		scope.ScopeType = defaultDrawingScopeType
	}
	scope.ScopeID = strings.TrimSpace(scope.ScopeID)
	if scope.ScopeID == "" {
		scope.ScopeID = defaultDrawingScopeID
	}
	if scope.ScopeType != defaultDrawingScopeType || scope.ScopeID != defaultDrawingScopeID {
		return ChartDrawingScope{}, errors.New("only the local user drawing scope is supported")
	}
	normalized, err := NormalizeChartRequest(scope.Request, now)
	if err != nil {
		return ChartDrawingScope{}, err
	}
	scope.Request = normalized
	return scope, nil
}

func drawingScopeQuery(database *gorm.DB, scope ChartDrawingScope) *gorm.DB {
	return database.Where("scope_type = ? AND scope_id = ? AND asset_type = ? AND market = ? AND code = ? AND period = ? AND adjustment = ?",
		scope.ScopeType, scope.ScopeID, scope.Request.Instrument.AssetType, scope.Request.Instrument.Market, scope.Request.Instrument.Code,
		scope.Request.Period, scope.Request.Adjustment)
}

func emptyDrawingDocument(scope ChartDrawingScope) ChartDrawingDocument {
	return ChartDrawingDocument{Instrument: scope.Request.Instrument, Period: scope.Request.Period, Adjustment: scope.Request.Adjustment,
		Revision: 0, Drawings: []ChartDrawing{}, UpdatedAt: time.Time{}}
}

func drawingDocumentFromRow(scope ChartDrawingScope, row chartDrawingDocumentRow) (ChartDrawingDocument, error) {
	drawings := make([]ChartDrawing, 0)
	if err := json.Unmarshal([]byte(row.DrawingsJSON), &drawings); err != nil {
		return ChartDrawingDocument{}, fmt.Errorf("decode chart drawings: %w", err)
	}
	if row.DeletedAt != nil {
		drawings = []ChartDrawing{}
	}
	return ChartDrawingDocument{Instrument: scope.Request.Instrument, Period: scope.Request.Period, Adjustment: scope.Request.Adjustment,
		Revision: row.Revision, Drawings: drawings, DeletedAt: row.DeletedAt, UpdatedAt: row.UpdatedAt}, nil
}

func validateAndMarshalDrawings(drawings []ChartDrawing) ([]byte, error) {
	if drawings == nil {
		drawings = []ChartDrawing{}
	}
	if len(drawings) > maxChartDrawings {
		return nil, fmt.Errorf("drawings must not exceed %d", maxChartDrawings)
	}
	seen := make(map[string]struct{}, len(drawings))
	for index := range drawings {
		drawings[index].ID = strings.TrimSpace(drawings[index].ID)
		drawings[index].Type = strings.ToLower(strings.TrimSpace(drawings[index].Type))
		if drawings[index].ID == "" || len(drawings[index].ID) > 128 {
			return nil, fmt.Errorf("drawing id is required and must not exceed 128 bytes")
		}
		if _, exists := seen[drawings[index].ID]; exists {
			return nil, fmt.Errorf("duplicate drawing id %q", drawings[index].ID)
		}
		seen[drawings[index].ID] = struct{}{}
		if err := validateDrawingPoints(drawings[index]); err != nil {
			return nil, err
		}
	}
	payload, err := json.Marshal(drawings)
	if err != nil {
		return nil, err
	}
	if len(payload) > maxChartDrawingJSON {
		return nil, fmt.Errorf("drawing payload must not exceed %d bytes", maxChartDrawingJSON)
	}
	return payload, nil
}

func validateDrawingPoints(drawing ChartDrawing) error {
	minimum, maximum := 0, 0
	switch drawing.Type {
	case "measure", "trend_line", "ray", "fibonacci_retracement":
		minimum, maximum = 2, 2
	case "horizontal_line":
		minimum, maximum = 1, 1
	case "wave":
		minimum, maximum = 3, 64
	default:
		return fmt.Errorf("unsupported drawing type %q", drawing.Type)
	}
	if len(drawing.Points) < minimum || len(drawing.Points) > maximum {
		return fmt.Errorf("drawing %q has invalid point count", drawing.ID)
	}
	for _, point := range drawing.Points {
		if point.Time.IsZero() || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			return fmt.Errorf("drawing %q contains an invalid point", drawing.ID)
		}
	}
	return nil
}

func newDrawingDocumentID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "drawing-" + hex.EncodeToString(value), nil
}

func isSQLiteUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed")
}

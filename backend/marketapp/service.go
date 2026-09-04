package marketapp

import (
	"context"
	"errors"
	"time"

	"go-stock/backend/models"

	"github.com/coocood/freecache"
	"gorm.io/gorm"
)

// NewsProvider is the provider-facing read/fetch surface whose signatures
// already match the application contract. Embedding it avoids forwarding
// methods that add no behavior.
type NewsProvider interface {
	StockResearchReport(string, int) []any
	StockNotice(string) []any
	IndustryResearchReport(string, int) []any
	EMDictCode(string, *freecache.Cache) []any
	HotEvent(int) *[]models.HotEvent
	HotTopic(int) []any
	InvestCalendar(string) []any
	ClsCalendar() []any
	GetTelegraphList(string) *[]*models.Telegraph
	TelegraphList(int64) *[]models.Telegraph
	GetSinaNews(uint) *[]models.Telegraph
	TradingViewNews() *[]models.Telegraph
	GetIndustryMoneyRankSina(string, string) []map[string]any
	GetMoneyRankSina(string) []map[string]any
	GetStockMoneyTrendByDay(string, int) []map[string]any
}

type SnapshotProvider interface {
	LongTiger(string) *[]models.LongTigerRankData
	XUEQIUHotStock(int, string) *[]models.HotItem
	GlobalStockIndexes(uint) map[string]any
	GetIndustryRank(string, int) map[string]any
}

type Dependencies struct {
	Database        *gorm.DB
	News            NewsProvider
	Snapshots       SnapshotProvider
	AnalyzeNews     func(string, bool)
	StartSelfCheck  func(string)
	IsOpenDay       func(time.Time) bool
	IsOpenDayStrict func(time.Time) (bool, error)
}

type Service struct {
	NewsProvider
	dependencies Dependencies
}

func NewService(dependencies Dependencies) *Service {
	return &Service{NewsProvider: dependencies.News, dependencies: dependencies}
}

func (s *Service) PersistSyncedTelegraph(ctx context.Context, telegraph *models.Telegraph, tags []string) (bool, error) {
	if telegraph == nil {
		return false, nil
	}
	if s == nil || s.dependencies.Database == nil {
		return false, errors.New("main database is not initialized")
	}
	created := false
	err := s.dependencies.Database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		query := tx.Model(&models.Telegraph{})
		if telegraph.Title == "" {
			query = query.Where("content = ?", telegraph.Content)
		} else {
			query = query.Where("title = ?", telegraph.Title)
		}
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		if err := tx.Create(telegraph).Error; err != nil {
			return err
		}
		for _, name := range tags {
			if name == "rotating_light" || name == "loudspeaker" {
				continue
			}
			tag := &models.Tags{Name: name, Type: "subject"}
			if err := tx.Where("name = ? AND type = ?", name, "subject").FirstOrCreate(tag).Error; err != nil {
				return err
			}
			association := &models.TelegraphTags{TelegraphId: telegraph.ID, TagId: tag.ID}
			if err := tx.Where("telegraph_id = ? AND tag_id = ?", telegraph.ID, tag.ID).FirstOrCreate(association).Error; err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	return created, err
}

func (s *Service) AnalyzeNews(text string, save bool) {
	if s != nil && s.dependencies.AnalyzeNews != nil {
		s.dependencies.AnalyzeNews(text, save)
	}
}

func (s *Service) EnsureMarketDataSelfCheck(reason string) {
	if s != nil && s.dependencies.StartSelfCheck != nil {
		s.dependencies.StartSelfCheck(reason)
	}
}

func (s *Service) IsCNOpenTradeDay(day time.Time) bool {
	return s != nil && s.dependencies.IsOpenDay != nil && s.dependencies.IsOpenDay(day)
}

func (s *Service) IsCNOpenTradeDayStrict(day time.Time) (bool, error) {
	if s == nil || s.dependencies.IsOpenDayStrict == nil {
		return false, errors.New("trade calendar is unavailable")
	}
	return s.dependencies.IsOpenDayStrict(day)
}

func (s *Service) LongTigerRank(date string) *[]models.LongTigerRankData {
	if s == nil || s.dependencies.Snapshots == nil {
		empty := []models.LongTigerRankData{}
		return &empty
	}
	return s.dependencies.Snapshots.LongTiger(date)
}

func (s *Service) HotStock(marketType string, size int) *[]models.HotItem {
	if s == nil || s.dependencies.Snapshots == nil {
		empty := []models.HotItem{}
		return &empty
	}
	return s.dependencies.Snapshots.XUEQIUHotStock(size, marketType)
}

func (s *Service) RefreshTelegraphList(source string) *[]*models.Telegraph {
	if s == nil || s.NewsProvider == nil {
		empty := []*models.Telegraph{}
		return &empty
	}
	go s.TelegraphList(30)
	go s.GetSinaNews(30)
	go s.TradingViewNews()
	return s.GetTelegraphList(source)
}

func (s *Service) GlobalStockIndexes() map[string]any {
	if s == nil || s.dependencies.Snapshots == nil {
		return map[string]any{}
	}
	return s.dependencies.Snapshots.GlobalStockIndexes(30)
}

func (s *Service) GetIndustryRank(sort string, count int) []any {
	if s == nil || s.dependencies.Snapshots == nil {
		return []any{}
	}
	result := s.dependencies.Snapshots.GetIndustryRank(sort, count)
	if items, ok := result["data"].([]any); ok && items != nil {
		return items
	}
	return []any{}
}

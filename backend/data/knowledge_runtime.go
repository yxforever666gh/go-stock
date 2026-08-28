package data

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go-stock/backend/knowledge"
	"go-stock/backend/research"
	"go-stock/backend/research2"

	"gorm.io/gorm"
)

type knowledgeReportLoader struct{ database *gorm.DB }

func (loader knowledgeReportLoader) LoadResearchReport(ctx context.Context, ownerType, ownerID string) (knowledge.ResearchReport, error) {
	if loader.database == nil {
		return knowledge.ResearchReport{}, errors.New("database is not initialized")
	}
	switch ownerType {
	case knowledgeOwnerResearch1:
		run, err := research.NewRepository(loader.database).Analysis(ctx, ownerID)
		if err != nil {
			return knowledge.ResearchReport{}, err
		}
		content := strings.TrimSpace(run.FinalReport)
		if content == "" {
			content = strings.TrimSpace(strings.Join([]string{run.MarketReport, run.SectorReport, run.StockReport}, "\n\n"))
		}
		return knowledge.ResearchReport{Title: fmt.Sprintf("Research 1 报告 %s", run.ScheduledFor.Format("2006-01-02 15:04")), Content: content}, nil
	case knowledgeOwnerResearch2:
		run, err := research2.NewRepository(loader.database).GetRun(ctx, ownerID)
		if err != nil {
			return knowledge.ResearchReport{}, err
		}
		return knowledge.ResearchReport{Title: "Research 2 报告 " + run.TradingDate, Content: strings.TrimSpace(run.ReportMarkdown)}, nil
	default:
		return knowledge.ResearchReport{}, knowledge.ErrInvalidInput
	}
}

const (
	knowledgeOwnerResearch1 = "research1"
	knowledgeOwnerResearch2 = "research2"
)

func NewKnowledgeService(mainDB *gorm.DB) *knowledge.Service {
	if mainDB == nil {
		return nil
	}
	return knowledge.NewService(knowledge.NewRepository(mainDB), knowledgeReportLoader{database: mainDB})
}

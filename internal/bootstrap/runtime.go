package bootstrap

import (
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"go-stock/internal/service"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AppRuntime struct {
	Config   appconfig.AppConfig
	Services service.AppServices
}

func NewRuntime(cfg appconfig.AppConfig) AppRuntime {
	return AppRuntime{
		Config:   cfg,
		Services: service.NewAppServices(),
	}
}

func InitApplication(cfg appconfig.AppConfig) AppRuntime {
	EnsureRuntimeDirs(cfg)
	db.Init(cfg.DB.Path)
	data.InitAnalyzeSentiment()
	go AutoMigrate()
	return NewRuntime(cfg)
}

func InitCLIStorage(dataDir, dbPath string) (string, error) {
	if dataDir == "" {
		dataDir = "data"
	}
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "stock.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), os.ModePerm); err != nil {
		return "", err
	}
	db.Init(dbPath)
	if err := db.Dao.AutoMigrate(&data.Settings{}, &data.AIConfig{}); err != nil {
		return "", err
	}
	return dbPath, nil
}

func EnsureRuntimeDirs(cfg appconfig.AppConfig) {
	if cfg.Runtime.Dir != "" {
		checkDir(cfg.Runtime.Dir)
	}
	checkDir(cfg.RuntimePath("data"))
	checkDir(cfg.RuntimePath("logs"))
	checkDir(cfg.ExportBaseDir())
	dbFilePath := strings.TrimSpace(cfg.DBFilePath())
	if dbFilePath != "" && dbFilePath != ":memory:" {
		dbDir := filepath.Dir(dbFilePath)
		if dbDir != "." && dbDir != "" {
			checkDir(dbDir)
		}
	}
	minuteDBFilePath := strings.TrimSpace(cfg.MinuteDBFilePath())
	if minuteDBFilePath != "" && minuteDBFilePath != ":memory:" {
		minuteDBDir := filepath.Dir(minuteDBFilePath)
		if minuteDBDir != "." && minuteDBDir != "" {
			checkDir(minuteDBDir)
		}
	}
}

func AutoMigrate() {
	if err := db.Dao.AutoMigrate(
		&data.StockInfo{},
		&data.StockBasic{},
		&data.FollowedStock{},
		&data.IndexBasic{},
		&data.Settings{},
		&models.AIResponseResult{},
		&models.AgentChatSession{},
		&models.AgentChatMessage{},
		&models.StockInfoHK{},
		&models.StockInfoUS{},
		&data.FollowedFund{},
		&data.FundBasic{},
		&models.PromptTemplate{},
		&data.Group{},
		&data.GroupStock{},
		&models.Tags{},
		&models.Telegraph{},
		&models.TelegraphTags{},
		&models.LongTigerRankData{},
		&data.AIConfig{},
		&models.BKDict{},
		&models.WordAnalyze{},
		&models.SentimentResultAnalyze{},
		&models.AiRecommendStocks{},
		&models.AiRecommendOpeningReview{},
		&models.AiRecommendYieldState{},
		&models.AiRecommendYieldOverride{},
		&models.AiRecommendYieldRecordState{},
		&models.AiRecommendYieldMeta{},
		&models.AiRecommendMinuteBar{},
		&models.AiRecommendDailyBar{},
		&models.CronTaskRun{},
		&models.EmailSendLog{},
		&models.MarketSummaryRunDiagnostic{},
	); err != nil {
		logger.SugaredLogger.Errorf("auto migrate failed: %v", err)
		return
	}
	data.ResetInterruptedAiRecommendYieldTasksOnStartup()
	if _, err := data.RepairSameDayOnlyLegacySkippedRecommendations(time.Now()); err != nil {
		logger.SugaredLogger.Warnf("repair sameDayOnly legacy skipped recommendations failed: %v", err)
	}
}

func checkDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}
}

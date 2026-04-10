package bootstrap

import (
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"go-stock/internal/service"
	"os"
	"path/filepath"
	"strings"
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
	if dbFilePath == "" || dbFilePath == ":memory:" {
		return
	}
	dbDir := filepath.Dir(dbFilePath)
	if dbDir != "." && dbDir != "" {
		checkDir(dbDir)
	}
}

func AutoMigrate() {
	db.Dao.AutoMigrate(&data.StockInfo{})
	db.Dao.AutoMigrate(&data.StockBasic{})
	db.Dao.AutoMigrate(&data.FollowedStock{})
	db.Dao.AutoMigrate(&data.IndexBasic{})
	db.Dao.AutoMigrate(&data.Settings{})
	db.Dao.AutoMigrate(&models.AIResponseResult{})
	db.Dao.AutoMigrate(&models.AgentChatSession{})
	db.Dao.AutoMigrate(&models.AgentChatMessage{})
	db.Dao.AutoMigrate(&models.StockInfoHK{})
	db.Dao.AutoMigrate(&models.StockInfoUS{})
	db.Dao.AutoMigrate(&data.FollowedFund{})
	db.Dao.AutoMigrate(&data.FundBasic{})
	db.Dao.AutoMigrate(&models.PromptTemplate{})
	db.Dao.AutoMigrate(&data.Group{})
	db.Dao.AutoMigrate(&data.GroupStock{})
	db.Dao.AutoMigrate(&models.Tags{})
	db.Dao.AutoMigrate(&models.Telegraph{})
	db.Dao.AutoMigrate(&models.TelegraphTags{})
	db.Dao.AutoMigrate(&models.LongTigerRankData{})
	db.Dao.AutoMigrate(&data.AIConfig{})
	db.Dao.AutoMigrate(&models.BKDict{})
	db.Dao.AutoMigrate(&models.WordAnalyze{})
	db.Dao.AutoMigrate(&models.SentimentResultAnalyze{})
	db.Dao.AutoMigrate(&models.AiRecommendStocks{})
	db.Dao.AutoMigrate(&models.AiRecommendOpeningReview{})
	db.Dao.AutoMigrate(&models.AiRecommendYieldState{})
	db.Dao.AutoMigrate(&models.AiRecommendYieldOverride{})
	db.Dao.AutoMigrate(&models.AiRecommendYieldRecordState{})
	db.Dao.AutoMigrate(&models.AiRecommendYieldMeta{})
	db.Dao.AutoMigrate(&models.AiRecommendMinuteBar{})
	db.Dao.AutoMigrate(&models.CronTaskRun{})
	db.Dao.AutoMigrate(&models.EmailSendLog{})
}

func checkDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_ = os.MkdirAll(dir, os.ModePerm)
	}
}

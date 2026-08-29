package main

import (
	"context"
	"go-stock/backend/data"
	"go-stock/backend/logger"
	"go-stock/internal/bootstrap"
	"go-stock/internal/service"
	"sync"
	"time"

	"github.com/coocood/freecache"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// App struct
type App struct {
	ctx                    context.Context
	runtime                *runtimeCoordinator
	cache                  *freecache.Cache
	cron                   *cron.Cron
	cronEntrys             map[string]cron.EntryID
	cronEntrysMu           sync.RWMutex
	services               service.AppServices
	domReadyMu             sync.Mutex
	domReadyDone           bool
	schedulerErrorsMu      sync.Mutex
	schedulerErrors        []error
	themeRuntimeMu         sync.Mutex
	themeRuntime           themeLifecycleCollector
	themeFactory           themeLifecycleFactory
	themeClock             func() time.Time
	themeOpenTradeDay      func(time.Time) (bool, error)
	themeRunMu             sync.Mutex
	themeErrorsMu          sync.Mutex
	themeErrors            []error
	researchRuntimeMu      sync.RWMutex
	researchRuntime        *data.ResearchRuntime
	researchFactory        func(int) (*data.ResearchRuntime, error)
	research2RuntimeMu     sync.RWMutex
	research2Runtime       *data.Research2Runtime
	research2Factory       func(int) (*data.Research2Runtime, error)
	aiAnalysisRunMu        sync.Mutex
	aiAnalysisRunning      bool
	aiDeploymentRunMu      sync.Mutex
	aiDeploymentLeaseOwner string
	aiLifecycleRunMu       sync.Mutex
	research2RunMu         sync.Mutex
	research2TradeMu       sync.Mutex
	research2MetricMu      sync.Mutex
	research2EmailMu       sync.Mutex
}

const aiLifecycleEntryKey = "AIAnalysisLifecycleDue"
const aiDeploymentEntryKey = "AICapitalDeploymentDue"
const research2AnalysisEntryKey = "Research2Analysis0950"
const research2TradingEntryKey = "Research2TradingMinute"
const research2MetricsEntryKey = "Research2Metrics1505"
const research2EmailEntryKey = "Research2EmailDelivery"
const themeLifecycleEntryKey = "ThemeLifecycle1510"

// NewApp creates a new App application struct
func NewApp() *App {
	services, err := bootstrap.NewProductionServices()
	if err != nil {
		panic(err)
	}
	return NewAppWithServices(services)
}

func NewAppWithServices(services service.AppServices) *App {
	cacheSize := 512 * 1024
	cache := freecache.NewCache(cacheSize)
	c := cron.New(cron.WithSeconds())
	runtime := newRuntimeCoordinator(context.Background())
	return &App{
		ctx:                    runtime.Context(),
		runtime:                runtime,
		cache:                  cache,
		cron:                   c,
		cronEntrys:             make(map[string]cron.EntryID),
		services:               services,
		themeClock:             time.Now,
		themeOpenTradeDay:      data.IsCNOpenTradeDayStrict,
		researchFactory:        data.NewResearchRuntime,
		research2Factory:       data.NewResearch2Runtime,
		aiDeploymentLeaseOwner: "go-stock-" + uuid.NewString(),
	}
}

func NewAppWithRuntime(appRuntime bootstrap.AppRuntime) *App {
	app := NewAppWithServices(appRuntime.Services)
	if appRuntime.Clock != nil {
		app.themeClock = appRuntime.Clock.Now
	}
	app.configureThemeLifecycleRuntime(appRuntime.Storage.Main, appRuntime.Storage.Minute)
	app.researchFactory = func(configID int) (*data.ResearchRuntime, error) {
		return data.NewResearchRuntimeWithStorage(configID, appRuntime.Storage.Main, appRuntime.Storage.Minute)
	}
	app.research2Factory = func(configID int) (*data.Research2Runtime, error) {
		return data.NewResearch2RuntimeWithStorage(configID, appRuntime.Storage.Main, appRuntime.Storage.Minute)
	}
	return app
}

// isTradingDay 判断是否是交易日
func isTradingDay(date time.Time) bool {
	weekday := date.Weekday()
	// 判断是否是周末
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	// 这里可以添加具体的节假日判断逻辑
	// 例如：判断是否是春节、国庆节等
	return true
}

// isTradingTime 判断是否是交易时间
func isTradingTime(date time.Time) bool {
	if !isTradingDay(date) {
		return false
	}

	hour, minute, _ := date.Clock()

	// 判断是否在9:15到11:30之间
	if (hour == 9 && minute >= 15) || (hour == 10) || (hour == 11 && minute <= 30) {
		return true
	}

	// 判断是否在13:00到15:00之间
	if (hour == 13) || (hour == 14) || (hour == 15 && minute <= 0) {
		return true
	}

	return false
}

// IsHKTradingTime 判断当前时间是否在港股交易时间内
func IsHKTradingTime(date time.Time) bool {
	hour, minute, _ := date.Clock()

	// 开市前竞价时段：09:00 - 09:30
	if (hour == 9 && minute >= 0) || (hour == 9 && minute <= 30) {
		return true
	}

	// 上午持续交易时段：09:30 - 12:00
	if (hour == 9 && minute > 30) || (hour >= 10 && hour < 12) || (hour == 12 && minute == 0) {
		return true
	}

	// 下午持续交易时段：13:00 - 16:00
	if (hour == 13 && minute >= 0) || (hour >= 14 && hour < 16) || (hour == 16 && minute == 0) {
		return true
	}

	// 收市竞价交易时段：16:00 - 16:10
	if (hour == 16 && minute >= 0) || (hour == 16 && minute <= 10) {
		return true
	}
	return false
}

// IsUSTradingTime 判断当前时间是否在美股交易时间内
func IsUSTradingTime(date time.Time) bool {
	// 获取美国东部时区
	est, err := time.LoadLocation("America/New_York")
	var estTime time.Time
	if err != nil {
		estTime = date.Add(time.Hour * -12)
	} else {
		// 将当前时间转换为美国东部时间
		estTime = date.In(est)
	}

	// 判断是否是周末
	weekday := estTime.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}

	// 获取小时和分钟
	hour, minute, _ := estTime.Clock()

	// 判断是否在4:00 AM到9:30 AM之间（盘前）
	if (hour == 4) || (hour == 5) || (hour == 6) || (hour == 7) || (hour == 8) || (hour == 9 && minute < 30) {
		return true
	}

	// 判断是否在9:30 AM到4:00 PM之间（盘中）
	if (hour == 9 && minute >= 30) || (hour >= 10 && hour < 16) || (hour == 16 && minute == 0) {
		return true
	}

	// 判断是否在4:00 PM到8:00 PM之间（盘后）
	if (hour == 16 && minute > 0) || (hour >= 17 && hour < 20) || (hour == 20 && minute == 0) {
		return true
	}

	return false
}
func MonitorFundPrices(a *App) {
	for _, follow := range a.services.Fund.GetFollowedFund() {
		_, err := a.services.Fund.CrawlFundBasic(follow.Code)
		if err != nil {
			logger.SugaredLogger.Errorf("获取基金基本信息失败，基金代码：%s，错误信息：%s", follow.Code, err.Error())
			continue
		}
		a.services.Fund.CrawlFundNetEstimatedUnit(follow.Code)
		a.services.Fund.CrawlFundNetUnitValue(follow.Code)
	}
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	defer PanicHandler()
	// Perform your teardown here
	//os.Exit(0)
	logger.SugaredLogger.Infof("application shutdown Version:%s", Version)
}

//// checkChromeOnWindows 在 Windows 系统上检查谷歌浏览器是否安装
//func checkChromeOnWindows() bool {
//	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
//	if err != nil {
//		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
//		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
//		if err != nil {
//			return false
//		}
//		defer key.Close()
//	}
//	defer key.Close()
//	_, _, err = key.GetValue("Path", nil)
//	return err == nil
//}
//
//// checkEdgeOnWindows 在 Windows 系统上检查Edge浏览器是否安装，并返回安装路径
//func checkEdgeOnWindows() (string, bool) {
//	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
//	if err != nil {
//		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
//		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
//		if err != nil {
//			return "", false
//		}
//		defer key.Close()
//	}
//	defer key.Close()
//	path, _, err := key.GetStringValue("Path")
//	if err != nil {
//		return "", false
//	}
//	return path, true
//}

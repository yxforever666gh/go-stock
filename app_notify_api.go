package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"time"

	"go-stock/backend/logger"
	"go-stock/backend/models"

	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/mathutil"
	"github.com/duke-git/lancet/v2/strutil"
)

func (a *App) SendDingDingMessage(message string, stockCode string) string {
	ttl, _ := a.cache.TTL([]byte(stockCode))
	logger.SugaredLogger.Infof("stockCode %s ttl:%d", stockCode, ttl)
	if ttl > 0 {
		return ""
	}
	err := a.cache.Set([]byte(stockCode), []byte("1"), 60*5)
	if err != nil {
		logger.SugaredLogger.Errorf("set cache error:%s", err.Error())
		return ""
	}
	return a.services.Notify.SendDingDingMessage(message)
}

// SendDingDingMessageByType msgType 报警类型: 1 涨跌报警;2 股价报警 3 成本价报警
func (a *App) SendDingDingMessageByType(message string, stockCode string, msgType int) string {
	if !shouldTrackRealtimeStock(stockCode, time.Now()) {
		if strutil.HasPrefixAny(stockCode, []string{"SZ", "SH", "sh", "sz"}) {
			return "非A股交易时间"
		}
		if strutil.HasPrefixAny(stockCode, []string{"hk", "HK"}) {
			return "非港股交易时间"
		}
		return "非美股交易时间"
	}

	ttl, _ := a.cache.TTL([]byte(stockCode))
	if ttl > 0 {
		return ""
	}
	err := a.cache.Set([]byte(stockCode), []byte("1"), getMsgTypeTTL(msgType))
	if err != nil {
		logger.SugaredLogger.Errorf("set cache error:%s", err.Error())
		return ""
	}
	stockInfo := a.services.Stock.GetStoredStockInfo(stockCode)
	a.services.Notify.SendAlert("go-stock消息通知", getMsgTypeName(msgType), GenNotificationMsg(stockInfo), "")
	return a.services.Notify.SendDingDingMessage(message)
}

func GenNotificationMsg(stockInfo *models.StockInfo) string {
	price, err := convertor.ToFloat(stockInfo.Price)
	if err != nil {
		price = 0
	}
	preClose, err := convertor.ToFloat(stockInfo.PreClose)
	if err != nil {
		preClose = 0
	}

	var rf float64
	if preClose > 0 {
		rf = mathutil.RoundToFloat(((price-preClose)/preClose)*100, 2)
	}

	return "[" + stockInfo.Name + "] " + stockInfo.Price + " " + convertor.ToString(rf) + "% " + stockInfo.Date + " " + stockInfo.Time
}

// msgType : 1 涨跌报警(5分钟);2 股价报警(30分钟) 3 成本价报警(30分钟)
func getMsgTypeTTL(msgType int) int {
	switch msgType {
	case 1:
		return 60 * 5
	case 2, 3:
		return 60 * 30
	default:
		return 60 * 5
	}
}

func getMsgTypeName(msgType int) string {
	switch msgType {
	case 1:
		return "涨跌报警"
	case 2:
		return "股价报警"
	case 3:
		return "成本价报警"
	default:
		return "未知类型"
	}
}

// OpenURL
//
//	@Description:  跨平台打开默认浏览器
//	@receiver a
//	@param url
func (a *App) OpenURL(url string) {
	openExternalURL(a.ctx, url)
}

// SaveImage
//
//	@Description: 跨平台保存图片
//	@receiver a
//	@param name
//	@param base64Data
//	@return error
func (a *App) SaveImage(name, base64Data string) string {
	filePath, err := saveFileWithDialog(a.ctx, runtimeSaveFileOptions{
		Title:           "保存图片",
		DefaultFilename: name + "AI分析.png",
		Filters: []runtimeFileFilter{
			{
				DisplayName: "PNG 图片",
				Pattern:     "*.png",
			},
		},
	})
	if err != nil || filePath == "" {
		return "文件路径,无法保存。"
	}

	decodeString, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "文件内容异常,无法保存。"
	}

	err = os.WriteFile(filepath.Clean(filePath), decodeString, os.ModePerm)
	if err != nil {
		return "保存结果异常,无法保存。"
	}
	return filePath
}

// SaveWordFile
//
//	@Description: // 跨平台保存word
//	@receiver a
//	@param filename
//	@param base64Data
//	@return error
func (a *App) SaveWordFile(filename string, base64Data string) string {
	filePath, err := saveFileWithDialog(a.ctx, runtimeSaveFileOptions{
		Title:           "保存 Word 文件",
		DefaultFilename: filename,
		Filters: []runtimeFileFilter{
			{DisplayName: "Word 文件", Pattern: "*.docx"},
		},
	})
	if err != nil || filePath == "" {
		return "文件路径,无法保存。"
	}

	decodeString, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "文件内容异常,无法保存。"
	}
	err = os.WriteFile(filepath.Clean(filePath), decodeString, 0o777)
	if err != nil {
		return "保存结果异常,无法保存。"
	}
	return filePath
}

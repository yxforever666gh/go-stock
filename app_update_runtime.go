package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/cryptor"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/go-resty/resty/v2"
	"github.com/inconshreveable/go-update"
	"golang.org/x/exp/slices"
)

func (a *App) CheckUpdate(flag int) {
	if !appconfig.Load().Update.SelfUpdateEnabled {
		if flag == 1 {
			a.emitReleaseNews("当前版本："+Version, manualUpdateHint())
		}
		return
	}

	sponsorCode := strings.TrimSpace(a.GetConfig().SponsorCode)
	if err := a.loadSponsorInfo(sponsorCode); err != nil {
		logger.SugaredLogger.Error(err.Error())
		return
	}

	releaseVersion, err := a.fetchLatestReleaseVersion()
	if err != nil {
		logger.SugaredLogger.Errorf("get github release version error:%s", err.Error())
		if flag == 1 {
			a.emitReleaseNews("当前版本："+Version, "当前仓库尚未发布可用 Release，或更新接口暂时不可用。")
		}
		return
	}
	logger.SugaredLogger.Infof("releaseVersion:%+v", releaseVersion.TagName)

	if _, vipLevel, ok := a.resolveVIPDownloadURL(sponsorCode, "", releaseVersion); ok {
		level, _ := convertor.ToInt(vipLevel)
		a.VipLevel = level
		if level >= 2 {
			go a.syncNews()
		}
	}

	if releaseVersion.TagName == Version {
		if flag == 1 {
			a.emitReleaseNews("当前版本："+Version, "当前版本无更新")
		}
		return
	}

	a.enrichReleaseVersion(releaseVersion)
	if !(IsWindows() || IsMacOS()) {
		go emitEvent(a.ctx, "updateVersion", releaseVersion)
		return
	}

	downloadURL, _, ok := a.resolveVIPDownloadURL(sponsorCode, defaultReleaseDownloadURL(releaseVersion.TagName), releaseVersion)
	if !ok {
		return
	}
	commitMessage := strings.TrimSpace(releaseVersion.Commit.Message)
	a.emitReleaseNews("发现新版本："+releaseVersion.TagName, commitMessage)

	body, err := downloadReleaseBinary(downloadURL)
	if err != nil || len(body) < 1024*500 {
		a.emitReleaseNews("新版本："+releaseVersion.TagName, commitMessage+"\n新版本下载失败,请稍后重试或请前往 "+publicReleasesURL()+" 手动下载替换文件。")
		return
	}

	if err = update.Apply(bytes.NewReader(body), update.Options{}); err != nil {
		logger.SugaredLogger.Error("更新失败: ", err.Error())
		go emitEvent(a.ctx, "updateVersion", releaseVersion)
		return
	}

	a.emitReleaseNews("新版本："+releaseVersion.TagName, "版本更新完成,下次重启软件生效.")
}

func (a *App) loadSponsorInfo(sponsorCode string) error {
	sponsorCode = strings.TrimSpace(sponsorCode)
	if sponsorCode == "" {
		a.SponsorInfo = nil
		return nil
	}
	encrypted, err := hex.DecodeString(sponsorCode)
	if err != nil {
		return err
	}
	key, err := hex.DecodeString(BuildKey)
	if err != nil {
		return err
	}
	decrypt := cryptor.AesEcbDecrypt(encrypted, key)
	if len(decrypt) == 0 {
		a.SponsorInfo = nil
		return nil
	}
	info := make(map[string]any)
	if err = json.Unmarshal(decrypt, &info); err != nil {
		return err
	}
	a.SponsorInfo = info
	return nil
}

func (a *App) fetchLatestReleaseVersion() (*models.GitHubReleaseVersion, error) {
	releaseVersion := &models.GitHubReleaseVersion{}
	resp, err := resty.New().R().
		SetHeader("Accept", "application/vnd.github+json").
		SetHeader("X-GitHub-Api-Version", "2022-11-28").
		SetResult(releaseVersion).
		Get(latestReleaseAPIURL())
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty github release response")
	}
	if resp.IsError() {
		return nil, fmt.Errorf("github release api returned status %d", resp.StatusCode())
	}
	if strings.TrimSpace(releaseVersion.TagName) == "" {
		return nil, fmt.Errorf("latest release tag is empty")
	}
	return releaseVersion, nil
}

func (a *App) enrichReleaseVersion(releaseVersion *models.GitHubReleaseVersion) {
	if releaseVersion == nil || strings.TrimSpace(releaseVersion.TagName) == "" {
		return
	}
	tag := &models.Tag{}
	if _, err := resty.New().R().
		SetHeader("Accept", "application/vnd.github+json").
		SetHeader("X-GitHub-Api-Version", "2022-11-28").
		SetResult(tag).
		Get(releaseTagAPIURL(releaseVersion.TagName)); err == nil {
		releaseVersion.Tag = *tag
	}

	if strings.TrimSpace(releaseVersion.Tag.Object.Url) == "" {
		return
	}
	commit := &models.Commit{}
	if _, err := resty.New().R().
		SetResult(commit).
		Get(releaseVersion.Tag.Object.Url); err == nil {
		releaseVersion.Commit = *commit
	}
}

func defaultReleaseDownloadURL(tagName string) string {
	if IsMacOS() {
		return releaseDownloadURL(tagName, "go-stock-darwin-universal")
	}
	return releaseDownloadURL(tagName, "go-stock-windows-amd64.exe")
}

func downloadReleaseBinary(downloadURL string) ([]byte, error) {
	resp, err := resty.New().R().Get(downloadURL)
	if err != nil {
		return nil, err
	}
	return resp.Body(), nil
}

func (a *App) resolveVIPDownloadURL(sponsorCode string, downloadURL string, releaseVersion *models.GitHubReleaseVersion) (string, string, bool) {
	isVIP := false
	vipLevel := "0"
	sponsorCode = strings.TrimSpace(sponsorCode)
	if sponsorCode == "" {
		return downloadURL, vipLevel, true
	}
	if err := a.loadSponsorInfo(sponsorCode); err != nil {
		logger.SugaredLogger.Error(err.Error())
		return downloadURL, "0", true
	}
	if a.SponsorInfo == nil {
		return downloadURL, "0", true
	}

	vipLevel = sponsorInfoString(a.SponsorInfo, "vipLevel")
	vipStartTime, err := sponsorInfoTime(a.SponsorInfo, "vipStartTime")
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return downloadURL, vipLevel, true
	}
	vipEndTime, err := sponsorInfoTime(a.SponsorInfo, "vipEndTime")
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return downloadURL, vipLevel, true
	}
	vipAuthTime, err := sponsorInfoTime(a.SponsorInfo, "vipAuthTime")
	if err != nil {
		logger.SugaredLogger.Error(err.Error())
		return downloadURL, vipLevel, true
	}

	now := time.Now()
	if now.After(vipAuthTime) && now.After(vipStartTime) && now.Before(vipEndTime) {
		isVIP = true
	}

	if releaseVersion == nil {
		return downloadURL, vipLevel, isVIP
	}

	if IsWindows() {
		return resolvePlatformVIPDownloadURL(isVIP, a.SponsorInfo, "winDownUrl", downloadURL, releaseDownloadURL(releaseVersion.TagName, "go-stock-windows-amd64.exe"), releaseDownloadURL(releaseVersion.TagName, "go-stock-windows-amd64.exe")), vipLevel, isVIP
	}
	if IsMacOS() {
		return resolvePlatformVIPDownloadURL(isVIP, a.SponsorInfo, "macDownUrl", downloadURL, releaseDownloadURL(releaseVersion.TagName, "go-stock-darwin-universal"), releaseDownloadURL(releaseVersion.TagName, "go-stock-darwin-universal")), vipLevel, isVIP
	}

	return downloadURL, vipLevel, isVIP
}

func resolvePlatformVIPDownloadURL(isVIP bool, sponsorInfo map[string]any, field string, fallbackURL string, vipFallbackURL string, normalURL string) string {
	if isVIP {
		if customURL := sponsorInfoString(sponsorInfo, field); customURL != "" {
			return customURL
		}
		return vipFallbackURL
	}
	if strings.TrimSpace(fallbackURL) != "" {
		return fallbackURL
	}
	return normalURL
}

func sponsorInfoString(sponsorInfo map[string]any, key string) string {
	if sponsorInfo == nil {
		return ""
	}
	if raw, ok := sponsorInfo[key]; ok && raw != nil {
		return strings.TrimSpace(fmt.Sprint(raw))
	}
	return ""
}

func sponsorInfoTime(sponsorInfo map[string]any, key string) (time.Time, error) {
	value := sponsorInfoString(sponsorInfo, key)
	return time.ParseInLocation("2006-01-02 15:04:05", value, time.Local)
}

func (a *App) emitReleaseNews(timeLabel, content string) {
	go emitEvent(a.ctx, "newsPush", map[string]any{
		"time":    timeLabel,
		"isRed":   true,
		"source":  "go-stock",
		"content": content,
	})
}

func (a *App) syncNews() {
	defer PanicHandler()
	url := syncNewsURL(time.Now().Add(-24 * time.Hour).Unix())
	if url == "" {
		logger.SugaredLogger.Info("syncNews skipped: GO_STOCK_SYNC_NEWS_URL_TEMPLATE is not configured")
		return
	}
	logger.SugaredLogger.Infof("syncNews:%s", url)
	resp, err := resty.New().R().SetDoNotParseResponse(true).Get(url)
	if err != nil {
		logger.SugaredLogger.Errorf("syncNews error:%s", err.Error())
		return
	}
	if resp == nil || resp.RawBody() == nil {
		return
	}
	body := resp.RawBody()
	defer body.Close()

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		logger.SugaredLogger.Infof("Received data: %s", scanner.Text())
		news := &models.NtfyNews{}
		if err = json.Unmarshal(scanner.Bytes(), news); err != nil {
			return
		}
		if telegraph := buildSyncedTelegraph(news, a.services.AI.AnalyzeSentiment(news.Message).Description); telegraph != nil {
			if a.persistSyncedTelegraph(telegraph, news.Tags) && time.Since(*telegraph.DataTime) < 5*time.Minute {
				a.NewsPush(&[]models.Telegraph{*telegraph})
			}
		}
	}
}

func buildSyncedTelegraph(news *models.NtfyNews, sentiment string) *models.Telegraph {
	if news == nil || !isSyncedTelegraphSource(news.Tags) {
		return nil
	}
	dataTime := time.UnixMilli(int64(news.Time * 1000))
	return &models.Telegraph{
		Title:           news.Title,
		Content:         news.Message,
		DataTime:        &dataTime,
		IsRed:           slice.Contain(news.Tags, "rotating_light"),
		Time:            dataTime.Format("15:04:05"),
		Source:          syncedTelegraphSource(news.Tags),
		SentimentResult: sentiment,
	}
}

func isSyncedTelegraphSource(tags []string) bool {
	return slice.ContainAny(tags, []string{"外媒资讯", "财联社电报", "新浪财经", "外媒简讯", "外媒"})
}

func syncedTelegraphSource(tags []string) string {
	if slice.ContainAny(tags, []string{"外媒简讯", "外媒资讯", "外媒"}) {
		return "外媒"
	}
	if slices.Contains(tags, "财联社电报") {
		return "财联社电报"
	}
	if slices.Contains(tags, "新浪财经") {
		return "新浪财经"
	}
	return ""
}

func (a *App) persistSyncedTelegraph(telegraph *models.Telegraph, tags []string) bool {
	if telegraph == nil {
		return false
	}
	cnt := int64(0)
	if telegraph.Title == "" {
		db.Dao.Model(telegraph).Where("content=?", telegraph.Content).Count(&cnt)
	} else {
		db.Dao.Model(telegraph).Where("title=?", telegraph.Title).Count(&cnt)
	}
	if cnt > 0 {
		return false
	}
	db.Dao.Model(telegraph).Create(telegraph)
	a.persistTelegraphTags(telegraph, tags)
	return true
}

func (a *App) persistTelegraphTags(telegraph *models.Telegraph, tags []string) {
	if telegraph == nil {
		return
	}
	subjects := slice.Filter(tags, func(index int, item string) bool {
		return !(item == "rotating_light" || item == "loudspeaker")
	})
	for _, subject := range subjects {
		tag := &models.Tags{
			Name: subject,
			Type: "subject",
		}
		db.Dao.Model(tag).Where("name=? and type=?", subject, "subject").FirstOrCreate(tag)
		db.Dao.Model(models.TelegraphTags{}).Where("telegraph_id=? and tag_id=?", telegraph.ID, tag.ID).FirstOrCreate(&models.TelegraphTags{
			TelegraphId: telegraph.ID,
			TagId:       tag.ID,
		})
	}
}

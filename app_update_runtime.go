package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/slice"
	"github.com/go-resty/resty/v2"
	"golang.org/x/exp/slices"
)

func (a *App) checkUpdate(flag int) {
	if !appconfig.Load().Update.SelfUpdateEnabled {
		if flag == 1 {
			a.emitReleaseNews("当前版本："+Version, manualUpdateHint())
		}
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

	if releaseVersion.TagName == Version {
		if flag == 1 {
			a.emitReleaseNews("当前版本："+Version, "当前版本无更新")
		}
		return
	}

	a.enrichReleaseVersion(releaseVersion)
	go emitEvent(a.ctx, "updateVersion", releaseVersion)
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
				emitEvent(a.ctx, "newTelegraph", []models.Telegraph{*telegraph})
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
	created, err := a.services.Market.PersistSyncedTelegraph(context.Background(), telegraph, tags)
	if err != nil {
		logger.SugaredLogger.Errorf("persist synced telegraph failed: %v", err)
		return false
	}
	return created
}

package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultPublicRepoSlug = "yxforever666gh/go-stock"
	defaultBaseInfoBase   = "https://raw.githubusercontent.com/" + defaultPublicRepoSlug + "/main/build"
)

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func publicRepoURL() string {
	return strings.TrimRight(envOrDefault("GO_STOCK_PUBLIC_REPO_URL", "https://github.com/"+defaultPublicRepoSlug), "/")
}

func publicIssuesURL() string {
	return publicRepoURL() + "/issues"
}

func publicReleasesURL() string {
	return publicRepoURL() + "/releases"
}

func latestReleaseAPIURL() string {
	return envOrDefault("GO_STOCK_RELEASE_API_URL", "https://api.github.com/repos/"+defaultPublicRepoSlug+"/releases/latest")
}

func releaseTagAPIURL(tag string) string {
	base := strings.TrimRight(envOrDefault("GO_STOCK_RELEASE_TAG_API_BASE_URL", "https://api.github.com/repos/"+defaultPublicRepoSlug+"/git/ref/tags"), "/")
	return base + "/" + strings.TrimSpace(tag)
}

func releaseDownloadURL(tagName, assetName string) string {
	base := strings.TrimRight(envOrDefault("GO_STOCK_RELEASE_DOWNLOAD_BASE_URL", publicReleasesURL()+"/download"), "/")
	return fmt.Sprintf("%s/%s/%s", base, strings.TrimSpace(tagName), strings.TrimSpace(assetName))
}

func baseInfoURL(fileName string) string {
	base := strings.TrimRight(envOrDefault("GO_STOCK_BASEINFO_BASE_URL", defaultBaseInfoBase), "/")
	return base + "/" + strings.TrimSpace(fileName)
}

func shareUploadURL() string {
	return strings.TrimSpace(os.Getenv("GO_STOCK_SHARE_UPLOAD_URL"))
}

func syncNewsURL(since int64) string {
	template := strings.TrimSpace(os.Getenv("GO_STOCK_SYNC_NEWS_URL_TEMPLATE"))
	if template == "" {
		return ""
	}
	return strings.ReplaceAll(template, "{since}", fmt.Sprintf("%d", since))
}

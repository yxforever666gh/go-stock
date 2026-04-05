package data

import (
	"context"
	"strings"

	"github.com/chromedp/chromedp"
)

func newCrawlerExecAllocator(parent context.Context, path string, headless bool, userAgent string) (context.Context, context.CancelFunc) {
	return chromedp.NewExecAllocator(parent, crawlerExecAllocatorOptions(path, headless, userAgent)...)
}

func crawlerExecAllocatorOptions(path string, headless bool, userAgent string) []chromedp.ExecAllocatorOption {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", headless),
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
		chromedp.Flag("disable-javascript", false),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("enable-features", "NetworkService,NetworkServiceInProcess"),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-breakpad", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-features", "site-per-process,Translate,BlinkGenPropertyTrees"),
		chromedp.Flag("disable-hang-monitor", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
		chromedp.Flag("disable-popup-blocking", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("metrics-recording-only", true),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("enable-automation", true),
		chromedp.Flag("password-store", "basic"),
		chromedp.Flag("use-mock-keychain", true),
	}
	if strings.TrimSpace(userAgent) != "" {
		opts = append(opts, chromedp.UserAgent(userAgent))
	}
	if forceNoProxyForFetchEnabled() {
		opts = append(opts,
			chromedp.Flag("no-proxy-server", true),
			chromedp.Flag("proxy-server", "direct://"),
			chromedp.Flag("proxy-bypass-list", "*"),
		)
	}
	if strings.TrimSpace(path) != "" {
		opts = append([]chromedp.ExecAllocatorOption{chromedp.ExecPath(path)}, opts...)
	}
	return opts
}

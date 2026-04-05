package data

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
)

func forceNoProxyForFetchEnabled() bool {
	config := GetSettingConfig()
	if config == nil || config.Settings == nil {
		return true
	}
	return config.ForceNoProxyForFetch
}

func newFetchRestyClient() *resty.Client {
	client := resty.New()
	restyApplyFetchProxyPolicy(client)
	return client
}

func restyApplyFetchProxyPolicy(client *resty.Client) {
	if client == nil {
		return
	}
	if forceNoProxyForFetchEnabled() {
		restyApplyNoProxy(client)
		return
	}
	restyApplyProxyFromSettingsOrDisable(client)
}

func newNoProxyRestyClient() *resty.Client {
	client := resty.New()
	restyApplyNoProxy(client)
	return client
}

// 实时数据链路（如盘中行情、分钟线）必须直连，避免本地/系统代理引入
// 额外延迟、403、代理不可用等问题。
func newRealtimeRestyClient() *resty.Client {
	return newNoProxyRestyClient()
}

func restyApplyNoProxy(client *resty.Client) {
	if client == nil {
		return
	}
	httpClient := client.GetClient()
	if httpClient == nil {
		return
	}

	if transport, ok := httpClient.Transport.(*http.Transport); ok && transport != nil {
		cloned := transport.Clone()
		cloned.Proxy = nil
		client.SetTransport(cloned)
		return
	}

	// Fall back to default transport when the client transport is not a *http.Transport.
	cloned := http.DefaultTransport.(*http.Transport).Clone()
	cloned.Proxy = nil
	client.SetTransport(cloned)
}

func restyApplyProxyFromSettingsOrDisable(client *resty.Client) {
	if client == nil {
		return
	}
	config := GetSettingConfig()
	if config == nil || config.Settings == nil || !config.HttpProxyEnabled {
		restyApplyNoProxy(client)
		return
	}

	proxyURL := strings.TrimSpace(config.HttpProxy)
	if proxyURL == "" {
		restyApplyNoProxy(client)
		return
	}

	u, err := url.Parse(proxyURL)
	if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		logger.SugaredLogger.Warnf("invalid settings http proxy url=%q (need scheme://host:port); fallback to no-proxy: %v", proxyURL, err)
		restyApplyNoProxy(client)
		return
	}

	client.SetProxy(proxyURL)
}

package data

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"go-stock/backend/models"
	appconfig "go-stock/internal/config"
)

func TestMinuteProvidersCaptureCredentialsAndIgnoreRuntimeOverride(t *testing.T) {
	firstConfig := &models.SettingConfig{Settings: &models.Settings{
		PrivateMinuteAPIKey: "first-key", PrivateMinuteBaseURL: "https://first.invalid/api", PrivateMinuteTimeoutSec: 7,
		PrivateMinuteProxyMode: "settings", HttpProxyEnabled: true, HttpProxy: "http://127.0.0.1:1111",
	}}
	first := newMinuteProviders(firstConfig)
	second := newMinuteProviders(&models.SettingConfig{Settings: &models.Settings{PrivateMinuteAPIKey: "second-key", PrivateMinuteBaseURL: "https://second.invalid/api"}})
	firstConfig.PrivateMinuteAPIKey = "changed-after-start"
	firstConfig.HttpProxy = "http://127.0.0.1:9999"
	otherKey := "global-runtime-key"
	appconfig.SetRuntimeOverride(&appconfig.RuntimeOverride{DiemengAPIKey: &otherKey})
	t.Cleanup(appconfig.ResetRuntimeOverride)
	if first.diemengAPIKey() != "first-key" || second.diemengAPIKey() != "second-key" || first.diemengTimeout() != 7*time.Second {
		t.Fatal("minute credentials or timeout escaped their snapshot")
	}
	client := first.newDiemengClient()
	request, _ := http.NewRequest(http.MethodGet, "https://first.invalid/api/stock/history", nil)
	proxy, err := client.GetClient().Transport.(*http.Transport).Proxy(request)
	if err != nil || proxy.String() != "http://127.0.0.1:1111" {
		t.Fatal("minute proxy changed during task")
	}
}

func TestMinuteProviderCircuitAndSuspensionFailureDoNotCrossTasks(t *testing.T) {
	first, second := newMinuteProviders(nil), newMinuteProviders(nil)
	for _, provider := range []struct {
		name        string
		fail        func(error)
		firstCheck  func() error
		secondCheck func() error
	}{
		{"tencent", first.tencentMinuteCircuitRecordFailure, first.tencentMinuteCircuitCheck, second.tencentMinuteCircuitCheck},
		{"sina", first.sinaMinuteCircuitRecordFailure, first.sinaMinuteCircuitCheck, second.sinaMinuteCircuitCheck},
		{"akshare", first.akShareCircuitRecordFailure, first.akShareCircuitCheck, second.akShareCircuitCheck},
		{"private", first.diemengCircuitRecordFailure, first.diemengCircuitCheck, second.diemengCircuitCheck},
	} {
		provider.fail(errors.New("connection reset"))
		provider.fail(errors.New("connection reset"))
		if provider.firstCheck() == nil || provider.secondCheck() != nil {
			t.Fatalf("%s circuit not isolated", provider.name)
		}
	}
	first.setCachedDiemengSuspensions("600519.SH|2026-09-09", nil, errors.New("first denied"))
	if _, exists := second.getCachedDiemengSuspensions("600519.SH|2026-09-09"); exists {
		t.Fatal("suspension failure escaped task")
	}
}

func TestMinuteProviderInheritsCapturedProxyEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1111")
	t.Setenv("NO_PROXY", "")
	provider := newMinuteProviders(&models.SettingConfig{Settings: &models.Settings{PrivateMinuteProxyMode: "inherit"}})
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:2222")
	client := provider.newDiemengClient()
	request, _ := http.NewRequest(http.MethodGet, "https://provider.invalid/stock/history", nil)
	proxy, err := client.GetClient().Transport.(*http.Transport).Proxy(request)
	if err != nil || proxy == nil || proxy.String() != "http://127.0.0.1:1111" {
		t.Fatal("provider reread proxy environment during task")
	}
}

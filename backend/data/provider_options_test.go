package data

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"github.com/go-resty/resty/v2"
	"gorm.io/gorm"
)

func TestResearch2ExperimentalSettingsGateOnlyOptionalDependencies(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "empty.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, enabled := range []bool{false, true} {
		setting := &models.SettingConfig{Settings: &models.Settings{ExperimentalEvidenceEnabled: enabled}}
		dependencies, err := NewResearch2Dependencies(0, database, nil, setting)
		if err != nil {
			t.Fatal(err)
		}
		setting.ExperimentalEvidenceEnabled = !enabled
		collector := dependencies.Evidence.(*research2EvidenceCollector)
		if (collector.themes != nil) != enabled || (dependencies.Knowledge != nil) != enabled {
			t.Fatalf("experimental dependencies ignored captured setting %t", enabled)
		}
		if collector.market == nil || collector.sources == nil || collector.minuteWindows == nil || dependencies.Market == nil || dependencies.EvidenceStore == nil || dependencies.EvidenceBuild == nil {
			t.Fatal("experimental switch disabled a core market or evidence dependency")
		}
	}
}

func TestAIProxyEnvironmentIsCapturedAcrossTaskAttempts(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1111")
	t.Setenv("NO_PROXY", "")
	setting := &models.SettingConfig{Settings: &models.Settings{}}
	model := &models.AIConfig{ApiKey: "fixture", BaseUrl: "https://provider.invalid"}
	environment := os.Environ()
	first := NewOpenAiWithSettings(context.Background(), model, setting)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:2222")
	// ResearchAIClientOptions uses the task-entry environment again when a
	// later model attempt constructs a new provider.
	retry := newOpenAiWithEnvironment(context.Background(), model, setting, environment)
	nextTask := NewOpenAiWithSettings(context.Background(), model, setting)
	model.HttpProxyEnabled, model.HttpProxy = true, "http://127.0.0.1:3333"
	explicit := NewOpenAiWithSettings(context.Background(), model, setting)
	for _, scenario := range []struct {
		name   string
		client *resty.Client
		want   string
	}{
		{"captured-chat", first.newAIClient(), "http://127.0.0.1:1111"},
		{"captured-anthropic", first.newAnthropicClient(), "http://127.0.0.1:1111"},
		{"later-attempt", retry.newAIClient(), "http://127.0.0.1:1111"},
		{"next-task", nextTask.newAIClient(), "http://127.0.0.1:2222"},
		{"explicit-chat", explicit.newAIClient(), "http://127.0.0.1:3333"},
		{"explicit-anthropic", explicit.newAnthropicClient(), "http://127.0.0.1:3333"},
		{"direct-chat-fallback", explicit.newAIClientWithProxy(false), ""},
		{"direct-anthropic-fallback", explicit.newAnthropicClientWithProxy(false), ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			transport := scenario.client.GetClient().Transport.(*http.Transport)
			if scenario.want == "" && transport.Proxy != nil {
				t.Fatal("direct fallback retained a proxy callback")
			}
			scenario.client.SetTransport(checkedMarketRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				actual := ""
				if transport.Proxy != nil {
					proxy, err := transport.Proxy(request)
					if err != nil {
						return nil, err
					}
					if proxy != nil {
						actual = proxy.String()
					}
				}
				if actual != scenario.want {
					t.Errorf("selected proxy %q, want %q", actual, scenario.want)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: request}, nil
			}))
			if _, err := scenario.client.R().Get("https://provider.invalid/probe"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

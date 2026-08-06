package agent

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go-stock/backend/data"
	applogger "go-stock/backend/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestGetStockAiAgentDoesNotLogAPIKey(t *testing.T) {
	const secret = "agent-log-secret-7f32"
	var output bytes.Buffer
	oldLogger := applogger.Logger
	oldSugaredLogger := applogger.SugaredLogger
	t.Cleanup(func() {
		applogger.Logger = oldLogger
		applogger.SugaredLogger = oldSugaredLogger
	})

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(applogger.NewEncoderConfig()),
		zapcore.AddSync(&output),
		zapcore.DebugLevel,
	)
	applogger.Logger = zap.New(core)
	applogger.SugaredLogger = applogger.Logger.Sugar()

	ctx := context.Background()
	_ = GetStockAiAgent(&ctx, data.AIConfig{
		Name:        "log-test",
		BaseUrl:     "https://example.invalid/v1",
		ApiKey:      secret,
		ModelName:   "test-model",
		MaxTokens:   32,
		TimeOut:     1,
		Temperature: 0.1,
	}, fakeAgentToolDataProvider{})
	_ = applogger.Logger.Sync()

	if strings.Contains(output.String(), secret) {
		t.Fatalf("agent initialization log exposed API key: %s", output.String())
	}
}

package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go-stock/backend/data"
	"go-stock/backend/db"
)

func initTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "stock.db")
	db.Init(dbPath)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&data.Settings{}, &data.AIConfig{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	_ = db.Dao.Exec("DELETE FROM settings").Error
	_ = db.Dao.Exec("DELETE FROM ai_config").Error
}

type fakeCommandAIClient struct{}

func (*fakeCommandAIClient) NewChatStreamLite(string, string, string, bool) <-chan map[string]any {
	return nil
}

type recordingCommandAIResolver struct {
	client  CommandAIClient
	err     error
	options AIOptions
	calls   int
}

func (r *recordingCommandAIResolver) ResolveCommandAI(_ context.Context, options AIOptions) (CommandAIClient, error) {
	r.calls++
	r.options = options
	return r.client, r.err
}

func TestResolveAIForCommandDelegatesToResolver(t *testing.T) {
	client := &fakeCommandAIClient{}
	resolver := &recordingCommandAIResolver{client: client}
	opts := AIOptions{
		AIConfigID:  17,
		BaseURL:     "https://param.example.com",
		APIKey:      "param-key",
		Model:       "param-model",
		MaxTokens:   2048,
		Temperature: 0.5,
		Timeout:     120,
	}

	got, err := ResolveAIForCommand(context.Background(), resolver, opts)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got != client {
		t.Fatal("resolver client was not returned")
	}
	if resolver.calls != 1 || resolver.options != opts {
		t.Fatalf("resolver call = %d options = %+v, want 1 and %+v", resolver.calls, resolver.options, opts)
	}
}

func TestResolveAIForCommandPropagatesResolverError(t *testing.T) {
	want := errors.New("resolve failed")
	resolver := &recordingCommandAIResolver{err: want}
	_, err := ResolveAIForCommand(context.Background(), resolver, AIOptions{AIConfigID: 9})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestResolveAIForCommandRejectsMissingResolver(t *testing.T) {
	_, err := ResolveAIForCommand(context.Background(), nil, AIOptions{})
	if err == nil {
		t.Fatal("expected missing resolver error")
	}
}

func TestResolveFingerprint(t *testing.T) {
	initTestDB(t)

	got, err := ResolveFingerprint("from-flag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-flag" {
		t.Fatalf("flag priority failed: %s", got)
	}

	if err := db.Dao.Create(&data.Settings{QgqpBId: "from-db"}).Error; err != nil {
		t.Fatalf("seed settings failed: %v", err)
	}
	got, err = ResolveFingerprint("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-db" {
		t.Fatalf("db fallback failed: %s", got)
	}

	_ = db.Dao.Exec("DELETE FROM settings").Error
	_, err = ResolveFingerprint("")
	if err == nil {
		t.Fatal("expected error when qgqp_b_id missing")
	}
}

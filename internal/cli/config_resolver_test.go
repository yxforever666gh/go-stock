package cli

import (
	"context"
	"errors"
	"testing"
)

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

type fingerprintResolverFixture struct {
	value string
	err   error
	calls int
}

func (f *fingerprintResolverFixture) ResolveFingerprint() (string, error) {
	f.calls++
	return f.value, f.err
}

func TestResolveFingerprint(t *testing.T) {
	resolver := &fingerprintResolverFixture{value: "from-settings"}
	got, err := ResolveFingerprint("from-flag", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-flag" || resolver.calls != 0 {
		t.Fatalf("flag priority failed: value=%s calls=%d", got, resolver.calls)
	}

	got, err = ResolveFingerprint("", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from-settings" || resolver.calls != 1 {
		t.Fatalf("settings fallback failed: value=%s calls=%d", got, resolver.calls)
	}

	resolver.value = ""
	_, err = ResolveFingerprint("", resolver)
	if err == nil {
		t.Fatal("expected error when qgqp_b_id missing")
	}
}

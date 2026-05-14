package service

import (
	"errors"
	"testing"
	"time"

	"github.com/kleinai/backend/internal/model"
	"github.com/kleinai/backend/internal/provider"
)

func TestProviderCooldownGrokForbiddenIsTransient(t *testing.T) {
	err := errors.New(`grok upload HTTP 403: <!DOCTYPE html><html><head><title>Just a moment...</title></head></html>`)
	if got := providerCooldown(err); got != 0 {
		t.Fatalf("expected transient cooldown 0, got %s", got)
	}
}

func TestProviderCooldownRetryable429StillCooldowns(t *testing.T) {
	err := errors.New(`provider call: grok video HTTP 429: {"error":{"code":8,"message":"Too many requests"}}`)
	got := providerCooldown(err)
	if got < 30*time.Minute {
		t.Fatalf("expected 429 cooldown >= 30m, got %s", got)
	}
}

func TestShouldUseGPTWebRouteDoesNotRouteDefault1KThroughChatGPTWeb(t *testing.T) {
	params := map[string]any{
		"resolution": "1K",
		"ratio":      "1:1",
	}

	if shouldUseGPTWebRoute(params) {
		t.Fatalf("default 1K gpt-image-2 should require the Codex/Responses route")
	}
}

func TestMinGPTImage2TimeoutExtendsMultiImageEdit(t *testing.T) {
	task := &model.GenerationTask{
		Provider:  model.ProviderGPT,
		Kind:      string(provider.KindImage),
		ModelCode: "gpt-image-2",
		Count:     4,
	}

	if got := minGPTImage2Timeout(task, []string{"ref1", "ref2", "ref3", "ref4"}); got < 20*time.Minute {
		t.Fatalf("expected multi-image edit timeout >= 20m, got %s", got)
	}
}

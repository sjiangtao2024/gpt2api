package service

import (
	"testing"

	"github.com/kleinai/backend/internal/model"
	"github.com/kleinai/backend/internal/provider"
)

func TestExtractFirstJSONObjectFromMixedOutput(t *testing.T) {
	raw := `thinking about the image...
{"scene_type":"室内空间","optimized_prompt":"单张广告图","negative_prompt":"拼图"}
{"scene_type":"duplicate"}`

	got, ok := extractFirstJSONObject(raw)
	if !ok {
		t.Fatalf("expected JSON object")
	}
	if got != `{"scene_type":"室内空间","optimized_prompt":"单张广告图","negative_prompt":"拼图"}` {
		t.Fatalf("unexpected JSON: %s", got)
	}
}

func TestParsePromptOptimizationAcceptsFencedJSON(t *testing.T) {
	raw := "```json\n{\"scene_type\":\"产品摄影\",\"optimized_prompt\":\"高端商业摄影\",\"preserve\":[\"产品轮廓\"]}\n```"

	got, err := parsePromptOptimization(raw)
	if err != nil {
		t.Fatalf("parsePromptOptimization error: %v", err)
	}
	if got.SceneType != "产品摄影" || got.OptimizedPrompt != "高端商业摄影" {
		t.Fatalf("unexpected parse result: %#v", got)
	}
	if len(got.Preserve) != 1 || got.Preserve[0] != "产品轮廓" {
		t.Fatalf("unexpected preserve: %#v", got.Preserve)
	}
}

func TestShouldOptimizePromptRequiresEnabledGPTImageWithRefs(t *testing.T) {
	task := &model.GenerationTask{
		Provider:  model.ProviderGPT,
		Kind:      string(provider.KindImage),
		ModelCode: "gpt-image-2",
	}

	if !shouldOptimizePrompt(task, []string{"ref"}, true) {
		t.Fatalf("expected optimizer to run for enabled gpt-image-2 image task with refs")
	}
	if shouldOptimizePrompt(task, nil, true) {
		t.Fatalf("expected optimizer to skip task without refs")
	}
	if shouldOptimizePrompt(task, []string{"ref"}, false) {
		t.Fatalf("expected optimizer to skip when disabled")
	}
	task.ModelCode = "img-v3"
	if shouldOptimizePrompt(task, []string{"ref"}, true) {
		t.Fatalf("expected optimizer to skip non gpt-image-2 model")
	}
}

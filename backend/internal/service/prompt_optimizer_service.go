package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/kleinai/backend/internal/model"
	"github.com/kleinai/backend/internal/provider"
	"github.com/kleinai/backend/pkg/outbound"
)

const promptOptimizerEndpoint = "https://chatgpt.com/backend-api/codex/responses"

type PromptOptimizerService struct {
	db  *gorm.DB
	cfg *SystemConfigService
}

type PromptOptimizationRequest struct {
	Task       *model.GenerationTask
	Account    *model.Account
	Credential string
	ProxyURL   string
	Prompt     string
	Refs       []string
}

type PromptOptimizationBrief struct {
	DetectedScene   string   `json:"detected_scene"`
	SceneConfidence float64  `json:"scene_confidence"`
	SceneType       string   `json:"scene_type"`
	VisualSummary   string   `json:"visual_summary"`
	CommercialGoal  string   `json:"commercial_goal"`
	Preserve        []string `json:"preserve"`
	Changes         []string `json:"changes"`
	RiskNotes       []string `json:"risk_notes"`
	OptimizedPrompt string   `json:"optimized_prompt"`
	NegativePrompt  string   `json:"negative_prompt"`
}

type PromptOptimizationResult struct {
	Brief           PromptOptimizationBrief
	OptimizedPrompt string
	NegativePrompt  string
	Raw             string
}

func NewPromptOptimizerService(db *gorm.DB, cfg *SystemConfigService) *PromptOptimizerService {
	return &PromptOptimizerService{db: db, cfg: cfg}
}

func (s *PromptOptimizerService) Optimize(ctx context.Context, req PromptOptimizationRequest) (*PromptOptimizationResult, bool) {
	if s == nil || s.cfg == nil || !shouldOptimizePrompt(req.Task, req.Refs, s.cfg.ImagePromptOptimizerEnabled(ctx)) {
		return nil, false
	}
	start := time.Now()
	modelCode := s.cfg.ImagePromptOptimizerModel(ctx)
	mode := s.cfg.ImagePromptOptimizerMode(ctx)
	if !isCodexOAuthAccount(req.Account) {
		s.log(ctx, req, modelCode, mode, "skipped", "", "", "", "account is not codex oauth", time.Since(start))
		return nil, false
	}
	if strings.TrimSpace(req.Credential) == "" {
		s.log(ctx, req, modelCode, mode, "skipped", "", "", "", "missing credential", time.Since(start))
		return nil, false
	}

	timeout := s.cfg.ImagePromptOptimizerTimeout(ctx)
	octx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	content := []map[string]any{{"type": "input_text", "text": advertisingOptimizerPrompt(req.Prompt)}}
	for _, ref := range req.Refs {
		imageURL, err := optimizerImageURL(ref)
		if err != nil {
			s.log(ctx, req, modelCode, mode, "failed", "", "", "", err.Error(), time.Since(start))
			return nil, false
		}
		content = append(content, map[string]any{"type": "input_image", "image_url": imageURL})
	}

	body := map[string]any{
		"instructions":        "You are a senior advertising creative director and commercial image retoucher. Analyze reference images and produce only valid JSON.",
		"model":               modelCode,
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"reasoning":           map[string]any{"effort": "medium", "summary": "auto"},
		"input":               []map[string]any{{"type": "message", "role": "user", "content": content}},
		"include":             []string{"reasoning.encrypted_content"},
	}
	raw, err := callPromptOptimizer(octx, req.Credential, req.ProxyURL, body)
	if err != nil {
		s.log(ctx, req, modelCode, mode, "failed", "", "", "", err.Error(), time.Since(start))
		return nil, false
	}
	brief, err := parsePromptOptimization(raw)
	if err != nil {
		s.log(ctx, req, modelCode, mode, "failed", "", "", raw, err.Error(), time.Since(start))
		return nil, false
	}
	brief.OptimizedPrompt = strings.TrimSpace(brief.OptimizedPrompt)
	if brief.OptimizedPrompt == "" {
		s.log(ctx, req, modelCode, mode, "failed", "", "", raw, "empty optimized_prompt", time.Since(start))
		return nil, false
	}
	briefJSON, _ := json.Marshal(brief)
	s.log(ctx, req, modelCode, mode, "success", brief.OptimizedPrompt, brief.NegativePrompt, string(briefJSON), "", time.Since(start))
	return &PromptOptimizationResult{
		Brief:           brief,
		OptimizedPrompt: brief.OptimizedPrompt,
		NegativePrompt:  strings.TrimSpace(brief.NegativePrompt),
		Raw:             raw,
	}, true
}

func (s *PromptOptimizerService) log(ctx context.Context, req PromptOptimizationRequest, modelCode, mode, status, optimized, negative, briefJSON, errText string, latency time.Duration) {
	if s == nil || s.db == nil || req.Task == nil {
		return
	}
	if s.cfg != nil && !s.cfg.ImagePromptOptimizerLogBrief(ctx) {
		briefJSON = ""
	}
	row := &model.GenerationPromptOptimization{
		TaskID:         req.Task.TaskID,
		OptimizerModel: modelCode,
		Mode:           mode,
		OriginalPrompt: req.Prompt,
		Status:         status,
		LatencyMs:      latency.Milliseconds(),
	}
	if req.Account != nil {
		row.AccountID = &req.Account.ID
	}
	if optimized != "" {
		row.OptimizedPrompt = &optimized
	}
	if negative != "" {
		row.NegativePrompt = &negative
	}
	if briefJSON != "" {
		row.BriefJSON = &briefJSON
	}
	if errText != "" {
		v := truncate(errText, 4000)
		row.Error = &v
	}
	_ = s.db.WithContext(ctx).Create(row).Error
}

func shouldOptimizePrompt(t *model.GenerationTask, refs []string, enabled bool) bool {
	if !enabled || t == nil {
		return false
	}
	return t.Provider == model.ProviderGPT &&
		t.Kind == string(provider.KindImage) &&
		strings.EqualFold(t.ModelCode, "gpt-image-2") &&
		len(refs) > 0
}

func parsePromptOptimization(raw string) (PromptOptimizationBrief, error) {
	obj, ok := extractFirstJSONObject(raw)
	if !ok {
		return PromptOptimizationBrief{}, fmt.Errorf("optimizer returned no JSON object")
	}
	var out PromptOptimizationBrief
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		return PromptOptimizationBrief{}, fmt.Errorf("decode optimizer JSON: %w", err)
	}
	return out, nil
}

func extractFirstJSONObject(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	inString := false
	escaped := false
	depth := 0
	start := -1
	for i, r := range raw {
		if start >= 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' && inString {
				escaped = true
				continue
			}
			if r == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if r == '{' {
				depth++
			}
			if r == '}' {
				depth--
				if depth == 0 {
					return raw[start : i+len(string(r))], true
				}
			}
			continue
		}
		if r == '{' {
			start = i
			depth = 1
		}
	}
	return "", false
}

func optimizerImageURL(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty reference image")
	}
	if strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref, nil
	}
	if !strings.HasPrefix(ref, "/api/v1/gen/cached/") {
		return "", fmt.Errorf("unsupported reference image %q", ref)
	}
	root := strings.TrimSpace(os.Getenv("KLEIN_STORAGE_ROOT"))
	if root == "" {
		root = "/app/storage/public"
	}
	rel := strings.TrimPrefix(ref, "/api/v1/gen/cached/")
	full := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read reference image: %w", err)
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(full)))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func callPromptOptimizer(ctx context.Context, token, proxyURL string, body map[string]any) (string, error) {
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, promptOptimizerEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Originator", "codex-tui")
	httpReq.Header.Set("Connection", "Keep-Alive")
	httpReq.Header.Set("User-Agent", "codex-tui/0.118.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9 (codex-tui; 0.118.0)")
	client, err := outbound.NewClient(outbound.Options{ProxyURL: proxyURL, Timeout: 5 * time.Minute, Mode: outbound.ModeUTLS, Profile: outbound.ProfileChrome})
	if err != nil {
		return "", err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("optimizer http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("optimizer %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return parsePromptOptimizerSSE(resp.Body)
}

func parsePromptOptimizerSSE(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var ev any
		if json.Unmarshal([]byte(raw), &ev) != nil {
			continue
		}
		b.WriteString(extractOptimizerText(ev))
	}
	return b.String(), scanner.Err()
}

func extractOptimizerText(v any) string {
	switch x := v.(type) {
	case map[string]any:
		var out strings.Builder
		for k, v := range x {
			if (k == "delta" || k == "text" || k == "output_text") && fmt.Sprint(v) != "" {
				out.WriteString(fmt.Sprint(v))
			}
			out.WriteString(extractOptimizerText(v))
		}
		return out.String()
	case []any:
		var out strings.Builder
		for _, v := range x {
			out.WriteString(extractOptimizerText(v))
		}
		return out.String()
	default:
		return ""
	}
}

func advertisingOptimizerPrompt(userPrompt string) string {
	return `你是资深广告视觉总监和商业修图师。请阅读参考图片和用户修改需求，输出严格 JSON，不要输出 Markdown。
JSON 字段：
detected_scene: 自动识别的场景枚举，只能是 interior_design / product_photography / brand_poster / fashion_portrait / ecommerce_main_image / social_ad / other
scene_confidence: 0 到 1 的场景判断置信度
scene_type: 场景类型
visual_summary: 对参考图的客观描述
commercial_goal: 广告/商业目标
preserve: 必须保留的元素数组
changes: 需要修改的元素数组
risk_notes: 容易生成错误的风险数组
optimized_prompt: 给图片生成/编辑模型使用的中文专业提示词，必须强调单张成图、不要拼图、不要多视角排版
negative_prompt: 禁止项

要求：
1. 优先保持参考图主体、构图、材质、品牌/产品/空间结构一致。
2. 将用户的自然语言需求改写成广告行业可交付的图片编辑 brief。
3. 如果参考图有多张，除非用户明确要求拼图，否则 optimized_prompt 必须要求单张完整画面。
4. 不要编造无法从参考图判断的品牌信息。
5. 自动判断广告场景，不要要求用户选择模式。

用户需求：` + strings.TrimSpace(userPrompt)
}

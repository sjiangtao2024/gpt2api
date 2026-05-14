package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/kleinai/backend/internal/bootstrap"
	"github.com/kleinai/backend/internal/model"
	"github.com/kleinai/backend/internal/repo"
	"github.com/kleinai/backend/internal/service"
	"github.com/kleinai/backend/pkg/crypto"
	"github.com/kleinai/backend/pkg/jwtpayload"
)

const codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	deps, err := bootstrap.Init("promptprobe")
	if err != nil {
		die(err)
	}
	taskID := strings.TrimSpace(os.Getenv("PROBE_TASK_ID"))
	if taskID == "" {
		taskID = latestImageTaskID(ctx, deps)
	}
	prompt := strings.TrimSpace(os.Getenv("PROBE_PROMPT"))
	if prompt == "" {
		prompt = "吊顶四周加上灯光，2000k，办公桌后面炉子改成酒精壁炉，右边弧形那做个透井"
	}

	var task model.GenerationTask
	if err := deps.DB.WithContext(ctx).Where("task_id = ?", taskID).First(&task).Error; err != nil {
		die(fmt.Errorf("load task %s: %w", taskID, err))
	}
	var refs []string
	if task.RefAssets != nil {
		_ = json.Unmarshal([]byte(*task.RefAssets), &refs)
	}
	if len(refs) == 0 {
		die(fmt.Errorf("task %s has no ref_assets", taskID))
	}
	if len(refs) > 4 {
		refs = refs[:4]
	}

	var acc model.Account
	if err := deps.DB.WithContext(ctx).
		Where("provider = ? AND auth_type = ? AND status = ? AND deleted_at IS NULL", model.ProviderGPT, model.AuthTypeOAuth, model.AccountStatusEnabled).
		Order("id DESC").First(&acc).Error; err != nil {
		die(fmt.Errorf("load gpt oauth account: %w", err))
	}
	token, err := accessToken(ctx, deps, &acc)
	if err != nil {
		die(err)
	}

	content := []map[string]any{{
		"type": "input_text",
		"text": optimizerPrompt(prompt),
	}}
	for _, ref := range refs {
		u, err := dataURL(ref)
		if err != nil {
			die(err)
		}
		content = append(content, map[string]any{"type": "input_image", "image_url": u})
	}
	body := map[string]any{
		"instructions":        "You are a senior advertising creative director and commercial image retoucher. Analyze reference images and produce only valid JSON.",
		"model":               "gpt-5.5",
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"reasoning":           map[string]any{"effort": "medium", "summary": "auto"},
		"input": []map[string]any{{
			"type":    "message",
			"role":    "user",
			"content": content,
		}},
	}
	out, err := callCodexResponses(ctx, token, body)
	if err != nil {
		die(err)
	}
	fmt.Println(out)
}

func latestImageTaskID(ctx context.Context, deps *bootstrap.Deps) string {
	var task model.GenerationTask
	if err := deps.DB.WithContext(ctx).
		Where("kind = ? AND ref_assets IS NOT NULL", "image").
		Order("id DESC").First(&task).Error; err != nil {
		die(fmt.Errorf("find latest image task: %w", err))
	}
	return task.TaskID
}

func accessToken(ctx context.Context, deps *bootstrap.Deps, acc *model.Account) (string, error) {
	at, err := decryptOptional(deps.AES, acc.AccessTokenEnc)
	if err != nil {
		return "", err
	}
	if at != "" && !tokenExpired(at) {
		return at, nil
	}
	rt, err := decryptOptional(deps.AES, acc.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	if rt == "" {
		rt, err = decryptOptional(deps.AES, acc.CredentialEnc)
		if err != nil {
			return "", err
		}
	}
	if rt == "" {
		return "", errors.New("oauth account missing refresh token")
	}
	sysCfg := service.NewSystemConfigService(repo.NewSystemConfigRepo(deps.DB))
	tr, err := service.NewOpenAIOAuthService(sysCfg).RefreshToken(ctx, rt, codexClientID, "")
	if err != nil {
		return "", err
	}
	at = strings.TrimSpace(tr.AccessToken)
	enc, err := deps.AES.Encrypt([]byte(at))
	if err == nil {
		updates := map[string]any{"access_token_enc": enc, "last_refresh_at": time.Now().UTC()}
		if exp, ok := jwtpayload.ExpUnixFromJWT(at); ok {
			updates["access_token_expires_at"] = time.Unix(exp, 0).UTC()
		}
		_ = deps.DB.WithContext(ctx).Model(&model.Account{}).Where("id = ?", acc.ID).Updates(updates).Error
	}
	return at, nil
}

func decryptOptional(aes *crypto.AESGCM, data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	plain, err := aes.Decrypt(data)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(plain)), nil
}

func tokenExpired(token string) bool {
	exp, ok := jwtpayload.ExpUnixFromJWT(token)
	return !ok || time.Now().Add(2*time.Minute).Unix() >= exp
}

func dataURL(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "data:") {
		return ref, nil
	}
	if !strings.HasPrefix(ref, "/api/v1/gen/cached/") {
		return "", fmt.Errorf("unsupported ref %q", ref)
	}
	root := strings.TrimSpace(os.Getenv("KLEIN_STORAGE_ROOT"))
	if root == "" {
		root = "/app/storage/public"
	}
	rel := strings.TrimPrefix(ref, "/api/v1/gen/cached/")
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(path))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func callCodexResponses(ctx context.Context, token string, body map[string]any) (string, error) {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("User-Agent", "codex-tui/0.118.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9 (codex-tui; 0.118.0)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("codex responses %d: %s", resp.StatusCode, string(raw))
	}
	return parseTextSSE(resp.Body)
}

func parseTextSSE(r io.Reader) (string, error) {
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
		var ev map[string]any
		if json.Unmarshal([]byte(raw), &ev) != nil {
			continue
		}
		for _, key := range []string{"delta", "text"} {
			if s, ok := ev[key].(string); ok {
				b.WriteString(s)
			}
		}
		if typ, _ := ev["type"].(string); strings.Contains(typ, "completed") {
			if text := extractText(ev); text != "" && b.Len() == 0 {
				b.WriteString(text)
			}
		}
	}
	return b.String(), scanner.Err()
}

func extractText(v any) string {
	switch x := v.(type) {
	case map[string]any:
		var out strings.Builder
		for k, v := range x {
			if (k == "text" || k == "output_text") && fmt.Sprint(v) != "" {
				out.WriteString(fmt.Sprint(v))
			}
			out.WriteString(extractText(v))
		}
		return out.String()
	case []any:
		var out strings.Builder
		for _, v := range x {
			out.WriteString(extractText(v))
		}
		return out.String()
	default:
		return ""
	}
}

func optimizerPrompt(userPrompt string) string {
	return `你是资深广告视觉总监和商业修图师。请阅读参考图片和用户修改需求，输出严格 JSON，不要输出 Markdown。
JSON 字段：
scene_type: 场景类型
visual_summary: 对参考图的客观描述
commercial_goal: 广告/商业目标
preserve: 必须保留的元素数组
changes: 需要修改的元素数组
risk_notes: 容易生成错误的风险数组
optimized_prompt: 给图片生成/编辑模型使用的中文专业提示词，强调单张成图、不要拼图、不要多视角排版
negative_prompt: 禁止项

用户需求：` + userPrompt
}

func die(err error) {
	zap.S().Error(err)
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

# Advertising Prompt Optimizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional advertising prompt optimizer that uses GPT OAuth/Codex `gpt-5.5` vision analysis to improve reference-image generation prompts before `gpt-image-2` runs.

**Architecture:** The optimizer is an internal service called from `GenerationService.runTask` after account selection and credential refresh, before provider invocation. It writes an audit row per attempt and falls back to the original prompt on timeout, upstream failure, or JSON parse failure.

**Tech Stack:** Go services, GORM/MySQL migrations, existing GPT OAuth account pool, Codex Responses endpoint, existing system_config KV.

---

### Task 1: Persistence and Configuration

**Files:**
- Create: `backend/migrations/20260514173000_add_prompt_optimization.sql`
- Modify: `backend/internal/model/generation.go`
- Modify: `backend/internal/service/system_config_service.go`

- [ ] Add `generation_prompt_optimization` migration with task_id, model, original_prompt, optimized_prompt, brief_json, status, error, latency_ms, created_at.
- [ ] Add `model.GenerationPromptOptimization` GORM model.
- [ ] Add typed config helpers for `image.prompt_optimizer.*` with safe defaults: disabled, model `gpt-5.5`, timeout 60 seconds, mode `advertising_general`, log enabled.

### Task 2: Optimizer Service

**Files:**
- Create: `backend/internal/service/prompt_optimizer_service.go`
- Test: `backend/internal/service/prompt_optimizer_service_test.go`

- [ ] Add tests for JSON extraction from clean JSON, fenced JSON, and mixed reasoning text.
- [ ] Add tests for `shouldOptimizePrompt` only enabling GPT image tasks with references when config is enabled.
- [ ] Implement `PromptOptimizerService.Optimize` with Codex Responses call, data URL reference handling, strict JSON parsing, and DB logging.
- [ ] Ensure any failure returns original prompt behavior through `(result, false)` rather than failing generation.

### Task 3: Generation Integration

**Files:**
- Modify: `backend/internal/service/generation_service.go`
- Test: `backend/internal/service/generation_service_test.go`

- [ ] Add optimizer dependency to `GenerationService` constructor.
- [ ] In `runTask`, after credential/proxy resolution and before provider request, run optimizer when enabled.
- [ ] Use optimized prompt for provider request only; keep task original prompt unchanged for user history.
- [ ] Pass negative prompt into provider request if optimizer supplies one and task has no explicit negative prompt.

### Task 4: Verification and Deployment

**Files:**
- Build/test only.

- [ ] Run `go test ./internal/service ./internal/provider/gpt`.
- [ ] Deploy backend to remote with compose rebuild.
- [ ] Keep optimizer disabled by default.
- [ ] Enable config manually only for A/B validation.

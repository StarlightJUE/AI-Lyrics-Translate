package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-lyrics-translate/config"
)

func TestPromptsBuilding(t *testing.T) {
	// 1. 验证元提示词硬编码包含“严禁输出背景信息”与行数纪律
	promptHans := BuildSystemPrompt("zh-Hans", "", "", false)
	if !strings.Contains(promptHans, "严禁输出背景信息") {
		t.Errorf("expected promptHans to contain strict constraint against background info")
	}
	if !strings.Contains(promptHans, "严格行数对应") {
		t.Errorf("expected promptHans to contain line count constraint")
	}
	if !strings.Contains(promptHans, "意境传神胜于字面直译") {
		t.Errorf("expected default zh-Hans user prompt to be appended")
	}

	// 2. 验证用户自定义提示词拼接
	customUserPrompt := "请使用李清照宋词风格进行润色，追求婉约词意"
	customPrompt := BuildSystemPrompt("zh-Hans", customUserPrompt, "", false)
	if !strings.Contains(customPrompt, "严禁输出背景信息") {
		t.Errorf("meta prompt framework missing in custom prompt")
	}
	if !strings.Contains(customPrompt, customUserPrompt) {
		t.Errorf("custom user prompt not appended: %s", customPrompt)
	}

	// 2.1 验证自定义元提示词生效
	customMeta := "自定义元提示词框架：将歌词翻译为 {target}，注意押韵"
	customMetaResult := BuildSystemPrompt("zh-Hans", customUserPrompt, customMeta, false)
	if !strings.Contains(customMetaResult, "自定义元提示词框架：将歌词翻译为 简体中文") {
		t.Errorf("custom meta prompt not applied: %s", customMetaResult)
	}

	// 3. 验证日韩罗马音专用提示词
	romajiPrompt := BuildSystemPrompt("romaji", "", "", true)
	if !strings.Contains(romajiPrompt, "Hepburn Romaji") || !strings.Contains(romajiPrompt, "Revised Romanization") {
		t.Errorf("expected romaji prompt to contain Hepburn and Revised Romanization guidelines")
	}

	// 4. 验证用户输入消息构建（附带背景信息）
	lines := []string{"Hello", "World"}
	bgInfo := "【背景信息】歌曲名：《Test Song》，歌手/乐队：Test Artist"
	userContent := BuildUserContent(lines, bgInfo, 1)
	if !strings.Contains(userContent, bgInfo) {
		t.Errorf("userContent missing bgInfo: %s", userContent)
	}
	if !strings.Contains(userContent, "严禁在输出中包含背景信息") {
		t.Errorf("userContent missing background info warning")
	}
	if !strings.Contains(userContent, "Hello\nWorld") {
		t.Errorf("userContent missing lyrics lines: %s", userContent)
	}
}

func TestDualModelFailover(t *testing.T) {
	var deepseekCallCount int32
	var geminiCallCount int32

	// 创建模拟服务器：DeepSeek 接口模拟失败（500 错误），Gemini 接口模拟成功
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat/completions") {
			atomic.AddInt32(&deepseekCallCount, 1)
			http.Error(w, `{"error":{"message":"Rate limit exceeded or service unavailable"}}`, http.StatusTooManyRequests)
			return
		}

		if strings.Contains(r.URL.Path, "generateContent") {
			atomic.AddInt32(&geminiCallCount, 1)
			var req GeminiRequest
			_ = json.NewDecoder(r.Body).Decode(&req)

			// 模拟 Gemini 正常返回 2 行
			resp := GeminiResponse{}
			resp.Candidates = make([]struct {
				Content struct {
					Parts []GeminiPart `json:"parts"`
				} `json:"content"`
			}, 1)
			resp.Candidates[0].Content.Parts = []GeminiPart{
				{Text: "你好\n世界"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		LLMPriority: []string{"deepseek", "gemini"},
		LLMProviders: map[string]config.ProviderConfig{
			"deepseek": {
				Type:           "openai",
				BaseURL:        mockServer.URL,
				APIKey:         "sk-mock-deepseek",
				Model:          "deepseek-chat",
				TimeoutSeconds: 5,
				MaxRetries:     1,
			},
			"gemini": {
				Type:           "gemini",
				BaseURL:        mockServer.URL,
				APIKey:         "AIza-mock-gemini",
				Model:          "gemini-2.0-flash",
				TimeoutSeconds: 5,
				MaxRetries:     1,
			},
		},
	}

	client := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testLines := []string{"Hello", "World"}
	results, modelUsed, err := client.TranslateBatch(ctx, testLines, "en", "zh-Hans", "【背景信息】歌曲名：《Test》", false)
	if err != nil {
		t.Fatalf("TranslateBatch failed: %v", err)
	}

	// 验证 DeepSeek 被调用后失败，自动 Failover 到 Gemini
	if atomic.LoadInt32(&deepseekCallCount) == 0 {
		t.Errorf("expected deepseek to be called first")
	}
	if atomic.LoadInt32(&geminiCallCount) == 0 {
		t.Errorf("expected failover to gemini")
	}

	if !strings.Contains(modelUsed, "gemini") {
		t.Errorf("expected modelUsed to be gemini, got %s", modelUsed)
	}

	if len(results) != 2 || results[0] != "你好" || results[1] != "世界" {
		t.Errorf("unexpected results from gemini: %#v", results)
	}
}

func TestLiveTranslationWithDeepSeek(t *testing.T) {
	cfg := config.LoadConfig()
	provs := cfg.GetProviders()
	dsKey := provs["deepseek"].APIKey
	if dsKey == "" || dsKey == "your_deepseek_api_key" {
		t.Skip("跳过真实 API 测试：未配置有效的 DeepSeek API Key")
	}

	client := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testLines := []string{
		"Yesterday once more",
		"All my troubles seemed so far away",
	}

	result, _, err := client.TranslateBatch(ctx, testLines, "en", "zh", "【背景信息】歌曲名：《Yesterday Once More》，歌手/乐队：Carpenters", false)
	if err != nil {
		t.Fatalf("Live translation failed: %v", err)
	}

	if len(result) != len(testLines) {
		t.Fatalf("Expected %d lines, got %d: %#v", len(testLines), len(result), result)
	}

	t.Logf("原文:\n%s\n\n译文:\n%s", strings.Join(testLines, "\n"), strings.Join(result, "\n"))
}

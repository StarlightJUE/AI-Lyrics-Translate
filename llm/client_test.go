package llm

import (
	"context"
	"strings"
	"testing"
	"time"

	"ai-lyrics-translate/config"
)

func TestLiveTranslationWithDeepSeek(t *testing.T) {
	cfg := config.LoadConfig()
	if cfg.LLMAPIKey == "" || cfg.LLMAPIKey == "your_api_key_here" {
		t.Skip("跳过真实 API 测试：未配置有效的 LLM_API_KEY")
	}

	client := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testLines := []string{
		"Yesterday once more",
		"All my troubles seemed so far away",
	}

	result, err := client.TranslateBatch(ctx, testLines, "en", "zh")
	if err != nil {
		t.Fatalf("Live translation failed: %v", err)
	}

	if len(result) != len(testLines) {
		t.Fatalf("Expected %d lines, got %d: %#v", len(testLines), len(result), result)
	}

	t.Logf("原文:\n%s\n\n译文:\n%s", strings.Join(testLines, "\n"), strings.Join(result, "\n"))
}

func TestLiveTraditionalToSimplified(t *testing.T) {
	cfg := config.LoadConfig()
	if cfg.LLMAPIKey == "" || cfg.LLMAPIKey == "your_api_key_here" {
		t.Skip("跳过真实 API 测试：未配置有效的 LLM_API_KEY")
	}

	client := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 繁体中文歌词测试：验证繁转简指令及原文保留
	traditionalLines := []string{
		"愛你不是兩三天",
		"每天卻想你很多遍",
	}

	result, err := client.TranslateBatch(ctx, traditionalLines, "zh-Hant", "zh")
	if err != nil {
		t.Fatalf("Live traditional conversion failed: %v", err)
	}

	expectedLines := []string{
		"爱你不是两三天",
		"每天却想你很多遍",
	}

	t.Logf("繁体原文:\n%s\n\n处理结果:\n%s", strings.Join(traditionalLines, "\n"), strings.Join(result, "\n"))

	for i, exp := range expectedLines {
		if result[i] != exp {
			t.Errorf("Line %d expected %q, got %q", i, exp, result[i])
		}
	}
}

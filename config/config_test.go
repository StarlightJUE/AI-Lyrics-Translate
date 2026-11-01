package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDualLLMAndPriority(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("LLM_PRIORITY", "")

	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test_config.yaml")

	yamlContent := `
server:
  host: "127.0.0.1"
  port: "5050"

llm:
  priority:
    - "gemini"
    - "deepseek"
  providers:
    deepseek:
      type: "openai"
      base_url: "https://api.deepseek.com"
      api_key: "sk-deepseek-test"
      model: "deepseek-chat"
    gemini:
      type: "gemini"
      base_url: "https://generativelanguage.googleapis.com/v1beta"
      api_key: "AIza-gemini-test"
      model: "gemini-2.0-flash"

translation:
  chinese_target_default: "traditional"
  custom_prompt: "自定义诗意风格"
`
	if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write test config failed: %v", err)
	}

	cfg := LoadConfig(configFile)

	// 1. 验证优先级
	pri := cfg.GetPriority()
	if len(pri) != 2 || pri[0] != "gemini" || pri[1] != "deepseek" {
		t.Fatalf("unexpected priority: %v", pri)
	}

	// 2. 验证当前生效快捷字段指向首选 gemini
	if cfg.LLMModel != "gemini-2.0-flash" || cfg.LLMAPIKey != "AIza-gemini-test" {
		t.Fatalf("active model mismatch: model=%s, key=%s", cfg.LLMModel, cfg.LLMAPIKey)
	}

	// 3. 验证两个 provider 均正确解析
	provs := cfg.GetProviders()
	if provs["deepseek"].APIKey != "sk-deepseek-test" || provs["gemini"].APIKey != "AIza-gemini-test" {
		t.Fatalf("provider keys mismatch: %#v", provs)
	}

	// 4. 验证繁体默认
	if cfg.ResolveTargetLanguage("zh") != "zh-Hant" {
		t.Errorf("expected zh-Hant, got %s", cfg.ResolveTargetLanguage("zh"))
	}

	// 5. 验证更新优先级并保存
	newPri := []string{"deepseek", "gemini"}
	if err := cfg.UpdatePriorityAndSave(newPri); err != nil {
		t.Fatalf("UpdatePriorityAndSave failed: %v", err)
	}

	// 重新加载验证落盘
	reloaded := LoadConfig(configFile)
	if reloaded.GetPriority()[0] != "deepseek" {
		t.Errorf("expected reloaded priority[0] to be deepseek, got %s", reloaded.GetPriority()[0])
	}
	if reloaded.LLMModel != "deepseek-chat" {
		t.Errorf("expected active model deepseek-chat, got %s", reloaded.LLMModel)
	}

	// 6. 验证更新用户提示词并保存
	if err := cfg.UpdateUserPromptAndSave("全新的用户提示词"); err != nil {
		t.Fatalf("UpdateUserPromptAndSave failed: %v", err)
	}
	reloadedPrompt := LoadConfig(configFile)
	if reloadedPrompt.GetUserPrompt() != "全新的用户提示词" {
		t.Errorf("expected updated prompt, got %s", reloadedPrompt.GetUserPrompt())
	}
}

func TestConfigBackwardCompatibility(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("LLM_PRIORITY", "")

	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "legacy_config.yaml")

	legacyYaml := `
server:
  host: "0.0.0.0"
  port: "5000"

llm:
  base_url: "https://api.openai.com/v1"
  api_key: "sk-legacy-key"
  model: "gpt-4o-mini"
`
	if err := os.WriteFile(configFile, []byte(legacyYaml), 0644); err != nil {
		t.Fatalf("write legacy config failed: %v", err)
	}

	cfg := LoadConfig(configFile)
	// 应自动映射到 deepseek 并设为首选
	provs := cfg.GetProviders()
	ds := provs["deepseek"]
	if ds.BaseURL != "https://api.openai.com/v1" || ds.APIKey != "sk-legacy-key" || ds.Model != "gpt-4o-mini" {
		t.Fatalf("legacy mapping failed: %#v", ds)
	}
	if cfg.LLMModel != "gpt-4o-mini" {
		t.Fatalf("active model mismatch: %s", cfg.LLMModel)
	}
}

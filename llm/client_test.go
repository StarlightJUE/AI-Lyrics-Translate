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
	if !strings.Contains(userContent, "严禁在输出中包含此背景信息") {
		t.Errorf("userContent missing background info warning: %s", userContent)
	}
	if !strings.Contains(userContent, `"0": "Hello"`) || !strings.Contains(userContent, `"1": "World"`) {
		t.Errorf("userContent missing indexed JSON lyrics lines: %s", userContent)
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

// TestJSONIndexedAlignmentAndEmptyLines 验证 JSON 行号映射能精确对齐空行且在缺键时不会产生整体错位
func TestJSONIndexedAlignmentAndEmptyLines(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟模型外包了 markdown 代码块，并且第0行漏掉了键（中文标题）、第1行显式空行，第2行日语翻译、第3行中文原样照抄
		respJSON := "```json\n{\n  \"1\": \"\",\n  \"2\": \"真无聊\",\n  \"3\": \"装作很帅气的将台词抛出来\"\n}\n```"
		resp := ChatCompletionResponse{}
		resp.Choices = append(resp.Choices, struct {
			Message ChatMessage `json:"message"`
		}{Message: ChatMessage{Role: "assistant", Content: respJSON}})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		LLMPriority: []string{"deepseek"},
		LLMProviders: map[string]config.ProviderConfig{
			"deepseek": {
				Type:           "openai",
				BaseURL:        mockServer.URL,
				APIKey:         "sk-mock",
				Model:          "deepseek-chat",
				TimeoutSeconds: 5,
				MaxRetries:     1,
			},
		},
	}

	client := NewClient(cfg)
	lines := []string{
		"cop、乐正绫 - 碎梦",
		"作词：cop",
		"つまらなぃ",
		"装作很帅气的将台词抛出来",
	}

	results, _, err := client.TranslateBatch(context.Background(), lines, "ja", "zh-Hans", "", false)
	if err != nil {
		t.Fatalf("TranslateBatch failed: %v", err)
	}

	if len(results) != 4 {
		t.Fatalf("expected 4 lines, got %d: %#v", len(results), results)
	}
	// 1. 第0行虽被模型遗漏键，由于处于中文目标兜底判定，返回空行 ""，且绝不会让后续第2行串位！
	if results[0] != "" {
		t.Errorf("expected line 0 to be empty string, got %q", results[0])
	}
	if results[1] != "" {
		t.Errorf("expected line 1 to be empty string, got %q", results[1])
	}
	// 2. 第2行精准对应到索引 2
	if results[2] != "真无聊" {
		t.Errorf("expected line 2 to be '真无聊', got %q", results[2])
	}
	// 3. 第3行模型照抄原文，后处理自动置为空行 ""
	if results[3] != "" {
		t.Errorf("expected line 3 (identical to source) to be empty string, got %q", results[3])
	}
}

// TestSameLanguageAndSymbolPostProcessing 验证纯符号曲目 0 Token 短路以及罗马音模式保留英文原样
func TestSameLanguageAndSymbolPostProcessing(t *testing.T) {
	var apiCalled int32
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiCalled, 1)
		respJSON := `{"0": "hello world", "1": "kimi no na wa"}`
		resp := ChatCompletionResponse{}
		resp.Choices = append(resp.Choices, struct {
			Message ChatMessage `json:"message"`
		}{Message: ChatMessage{Role: "assistant", Content: respJSON}})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		LLMPriority: []string{"deepseek"},
		LLMProviders: map[string]config.ProviderConfig{
			"deepseek": {
				Type:           "openai",
				BaseURL:        mockServer.URL,
				APIKey:         "sk-mock",
				Model:          "deepseek-chat",
				TimeoutSeconds: 5,
				MaxRetries:     1,
			},
		},
	}

	client := NewClient(cfg)

	// 1. 全纯符号歌词：非罗马音模式下不调用 API 直接返回空行
	symRes, modelUsed, err := client.TranslateBatch(context.Background(), []string{"🎶🎶🎶", "..."}, "ja", "zh-Hans", "", false)
	if err != nil {
		t.Fatalf("TranslateBatch symbol failed: %v", err)
	}
	if atomic.LoadInt32(&apiCalled) != 0 {
		t.Errorf("expected 0 API calls for symbol-only lyrics, got %d", apiCalled)
	}
	if !strings.Contains(modelUsed, "local-skip") || len(symRes) != 2 || symRes[0] != "" || symRes[1] != "" {
		t.Errorf("unexpected symbol result: model=%s, res=%#v", modelUsed, symRes)
	}

	// 2. 罗马音模式下：即使与原文内容仅差大小写（如英文行），仍保留原转写结果不置空
	romRes, _, err := client.TranslateBatch(context.Background(), []string{"Hello World", "君の名は"}, "ja", "romaji", "", true)
	if err != nil {
		t.Fatalf("TranslateBatch romaji failed: %v", err)
	}
	if len(romRes) != 2 || romRes[0] != "hello world" || romRes[1] != "kimi no na wa" {
		t.Errorf("unexpected romaji result: %#v", romRes)
	}
}

// TestLiveSuiMengMixedChineseJapanese 用真实 DeepSeek API 验证《碎梦》全曲 47 行（含标题制作人员及夹杂单句日语）精确对齐与同语种置空
func TestLiveSuiMengMixedChineseJapanese(t *testing.T) {
	cfg := config.LoadConfig()
	provs := cfg.GetProviders()
	dsKey := provs["deepseek"].APIKey
	if dsKey == "" || dsKey == "your_deepseek_api_key" {
		t.Skip("跳过真实 API 测试：未配置有效的 DeepSeek API Key")
	}

	client := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	lines := []string{
		"cop、乐正绫 - 碎梦",
		"作词：cop",
		"作曲：cop",
		"演唱：乐正绫",
		"事到如今 懒得想什么明天",
		"抬眼只剩下没有转晴迹象的阴霾",
		"招朋引伴 尽兴后匆忙四散",
		"道别也不忍将偶像乏味感言打断",
		"百无聊赖",
		"将折成了纸飞机的传单展开",
		"为奔波生计的油墨外",
		"有潦草勾画的未来",
		"从我又小又拥挤的房间",
		"从我堆满了贪念的床边",
		"飞向总有容身处的世界",
		"却碎裂 却碎裂",
		"在那四处是虫鸣的夏夜",
		"在那潮湿又安静的街边",
		"笨拙打着拍子的陌生人",
		"都不见",
		"不忍看未来 尽是歪扭的残骸",
		"满天的阴霾降下为我覆上了尘埃",
		"满纸剖白 讽笑后不再理睬",
		"怎会为空泛的随口戏言徒增感慨",
		"つまらなぃ",
		"装作很帅气的将台词抛出来",
		"如今除了感到好笑外",
		"是对懦弱自己的悲哀",
		"为我又小又拥挤的房间",
		"为我堆满了床边的贪念",
		"以为总有容身处的世界",
		"却碎裂 却碎裂",
		"在那两个人畅谈的夏夜",
		"弦音如同鸥鸟飞离云边",
		"词语诉说着由衷的喜欢",
		"都不见 不能再见",
		"分不清热泪和雨的终点",
		"站在一步之遥倾塌面前",
		"才发现被亲手推开的人 曾在我身边",
		"事到如今 终于理解了明天",
		"是被打碎的梦想重新拼凑的情节",
		"如何安排 都指向这扇门外",
		"让时间再次转动的时针预备摇摆",
		"迈过碎块",
		"我怀抱着美梦坠地后的碎块",
		"除开道别的说辞外",
		"原谅我最后的逃开",
	}

	// 注意这里特意像真实客户端一样传入错误的 sourceLang="ja"，测试是否能准确辨识并只在第24行输出中文译文
	result, modelUsed, err := client.TranslateBatch(ctx, lines, "ja", "zh-Hans", "", false)
	if err != nil {
		t.Fatalf("Live translation failed: %v", err)
	}
	t.Logf("模型: %s, 总行数: %d", modelUsed, len(result))

	if len(result) != len(lines) {
		t.Fatalf("期望 %d 行, 实际 %d 行", len(lines), len(result))
	}

	for i, r := range result {
		if i == 24 {
			if r == "" {
				t.Errorf("第 24 行日语 'つまらなぃ' 应该被翻译为中文，却返回了空行")
			} else {
				t.Logf("第 24 行成功翻译: %q -> %q", lines[i], r)
			}
		} else {
			if r != "" {
				t.Errorf("第 %d 行中文 %q 期望返回空行，实际返回 %q", i, lines[i], r)
			}
		}
	}
}

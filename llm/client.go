package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ai-lyrics-translate/config"
)

const (
	// DefaultSystemPromptZhHans 简体中文文学级词作调优提示词
	DefaultSystemPromptZhHans = `你是一位殿堂级的音乐词作家与文学翻译大师，擅长将外语歌词以富有诗意、音律感和画面感的中文重新诠释。请将歌词翻译为「简体中文」（遵循中国大陆当代文学歌词规范）。

【核心审美追求（坚决杜绝平庸直白）】：
1. 意境传神胜于字面直译：深入领会整首歌词的情感张力与哲思，重在重塑诗意美感与宏大画面，严禁使用干瘪的大白话、说明文或机械直译腔。
2. 词句雅致富有灵气：遣词典雅考究，融入中文歌词独有的含蓄与诗意（例如：“荒漠亦曾浩瀚成海”远优于“一些沙漠曾经是海洋”；“愿所有美好皆被温柔以待”远优于“希望所有美丽都被祝福”）。
3. 音律与节奏感：译文需读来琅琅上口、语感凝练，符合中文音乐听觉美学。

【硬性约束】：
1. 源语言自主识别：自行辨别歌词实际语种（严禁盲从客户端可能误判的 source 设定，准确识别拉丁文、法语、德语、西班牙语、日语罗马音等）。
2. 中文原文保护：
   - 若原文已是简体中文，直接原样输出；
   - 若原文为繁体中文，严格仅做字形繁转简，严禁改动原作者用词与曲意。
3. 严格行数对应：输入多少行就必须输出多少行，每行严格 1 对 1 翻译，严禁合并、拆分或遗漏。
4. 纯净输出：仅输出翻译后的歌词文本，绝不包含任何前缀、解释、注释或代码块。`

	// DefaultSystemPromptZhHant 繁体中文文学级词作调优提示词
	DefaultSystemPromptZhHant = `你是一位殿堂級的音樂詞作家與文學翻譯大師，擅長將外語歌詞以富有詩意、音律感和畫面感的中文重新詮釋。請將歌詞翻譯為「繁體中文」（遵循港台或通用繁體中文文學歌詞規範）。

【核心審美追求（堅決杜絕平庸直白）】：
1. 意境傳神勝於字面直譯：深入領會整首歌曲的情感張力與哲思，重在重塑詩意美感與宏大畫面，嚴禁使用乾癟的大白話、說明文或機械直譯腔。
2. 詞句雅致富有靈氣：遣詞典雅考究，融入中文歌詞獨有的含蓄與詩意（例如：「荒漠亦曾浩瀚成海」遠優於「一些沙漠曾經是海洋」；「願所有美好皆被溫柔以待」遠優於「希望所有美麗都被祝福」）。
3. 音律與節奏感：譯文需讀來琅琅上口、語感凝練，符合中文音樂聽覺美學。

【硬性約束】：
1. 源語言自主識別：自行辨別歌詞實際語種（嚴禁盲從客戶端可能誤判的 source 設定，準確識別拉丁文、法語、德語、西班牙語、日語羅馬音等）。
2. 中文原文保護：
   - 若原文已是繁體中文，直接原樣輸出；
   - 若原文為簡體中文，嚴格僅做字形簡轉繁，嚴禁改動原作者用詞與曲意。
3. 嚴格行數對應：輸入多少行就必須輸出多少行，每行嚴格 1 對 1 翻譯，嚴禁合併、拆分或遺漏。
4. 純淨輸出：僅輸出翻譯後的歌詞文本，絕不包含任何前綴、解釋、注釋或代碼塊。`
)

// Client 负责与 OpenAI 兼容格式的大模型进行交互
type Client struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewClient 初始化 LLM 客户端
func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.LLMTimeoutSeconds) * time.Second,
		},
	}
}

// ChatMessage OpenAI 消息结构
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest 请求载荷
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

// ChatCompletionResponse 响应载荷
type ChatCompletionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// TranslateBatch 批量翻译歌词行，内置重试与行数校验
func (c *Client) TranslateBatch(ctx context.Context, cleanLines []string, sourceLang, targetLang string) ([]string, error) {
	if len(cleanLines) == 0 {
		return []string{}, nil
	}

	inputText := strings.Join(cleanLines, "\n")
	systemPrompt := c.buildSystemPrompt(targetLang)

	var lastErr error
	maxRetries := c.cfg.LLMMaxRetries
	if maxRetries < 1 {
		maxRetries = 1
	}

	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		userPrompt := inputText
		if attempt > 1 {
			// 重试时强调行数一致性
			userPrompt = fmt.Sprintf("【注意：必须输出严格 %d 行，每一行一一对应，不要增加或减少任何一行】\n\n%s", len(cleanLines), inputText)
		}

		translatedRaw, err := c.callAPI(ctx, systemPrompt, userPrompt)
		if err != nil {
			lastErr = err
			log.Printf("[WARN] LLM API 调用失败 (尝试 %d/%d): %v", attempt, maxRetries+1, err)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		// 结果清洗与行对齐校验
		resultLines := cleanOutputLines(translatedRaw)

		if len(resultLines) == len(cleanLines) {
			return resultLines, nil
		}

		log.Printf("[WARN] LLM 返回行数不一致 (期望 %d 行，实际 %d 行)，准备校验纠偏", len(cleanLines), len(resultLines))

		// 若已是最后一次尝试，执行自动补齐/截断对齐兜底，确保不会崩溃
		if attempt == maxRetries+1 {
			aligned := alignLines(resultLines, cleanLines)
			return aligned, nil
		}
	}

	return nil, fmt.Errorf("llm translate failed after retries: %w", lastErr)
}

func (c *Client) buildSystemPrompt(targetLang string) string {
	// 1. 若配置了用户自定义提示词，最高优先级使用
	if c.cfg.CustomPrompt != "" {
		prompt := c.cfg.CustomPrompt
		prompt = strings.ReplaceAll(prompt, "{target}", targetLang)
		return prompt
	}

	target := strings.ToLower(strings.TrimSpace(targetLang))

	// 2. 繁体中文目标语言 (zt, zh-hant, zh-tw, zh-hk)
	if target == "zh-hant" || target == "zt" || target == "zh-tw" || target == "zh-hk" {
		return DefaultSystemPromptZhHant
	}

	// 3. 简体中文目标语言 (zh-hans, zh-cn, chs, zh 或默认)
	if target == "zh-hans" || target == "zh-cn" || target == "chs" || target == "zh" || target == "" {
		return DefaultSystemPromptZhHans
	}

	// 4. 其他目标语言通用提示词（同样要求自主识别源语言，不依赖客户端假定）
	return fmt.Sprintf("You are a professional lyrics translator. Translate the lyrics into target language '%s'.\nIdentify the actual source language of the lyrics yourself (do not rely on or assume any external source language setting, accurately detect Latin, French, German, Spanish, Japanese Romaji, etc.).\nKeep the literary quality, poetic elegance, and emotional nuance. Maintain strict one-to-one line correspondence, never merge or split lines. Output only the translated lines without any markdown, annotations, or prefixes.", targetLang)
}

func (c *Client) callAPI(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqURL := strings.TrimRight(c.cfg.LLMBaseURL, "/")
	if !strings.HasSuffix(reqURL, "/chat/completions") {
		reqURL += "/chat/completions"
	}

	payload := ChatCompletionRequest{
		Model: c.cfg.LLMModel,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: c.cfg.LLMTemperature,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.cfg.LLMAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.LLMAPIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api responded with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal api response failed: %w", err)
	}

	if chatResp.Error != nil && chatResp.Error.Message != "" {
		return "", fmt.Errorf("api error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty choices returned from llm")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// cleanOutputLines 清理可能包裹的 markdown 代码块及首尾空白
func cleanOutputLines(raw string) []string {
	content := strings.TrimSpace(raw)
	// 去除可能出现的 ``` 或 ```markdown / ```text
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 2 {
			if strings.HasPrefix(lines[0], "```") {
				lines = lines[1:]
			}
			if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
				lines = lines[:len(lines)-1]
			}
			content = strings.Join(lines, "\n")
		}
	}

	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	rawLines := strings.Split(content, "\n")
	var result []string
	for _, l := range rawLines {
		result = append(result, strings.TrimSpace(l))
	}

	// 剔除末尾多余的空行
	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}

	return result
}

// alignLines 对齐处理：保证返回行数与源歌词行数绝对一致
func alignLines(actual []string, original []string) []string {
	targetLen := len(original)
	aligned := make([]string, targetLen)

	for i := 0; i < targetLen; i++ {
		if i < len(actual) && strings.TrimSpace(actual[i]) != "" {
			aligned[i] = actual[i]
		} else {
			// 若不足，回退保留原文
			aligned[i] = original[i]
		}
	}
	return aligned
}

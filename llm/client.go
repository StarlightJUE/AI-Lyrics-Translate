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

// Client 负责多模型（DeepSeek 与 Gemini 等）的统一调度、重试校验与自动故障切换
type Client struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewClient 初始化 LLM 客户端
func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // 容纳大模型批量请求与网络延迟
		},
	}
}

// OpenAI 请求/响应载荷定义
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Gemini 请求/响应载荷定义
type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiRequest struct {
	SystemInstruction *GeminiContent  `json:"systemInstruction,omitempty"`
	Contents          []GeminiContent `json:"contents"`
	GenerationConfig  struct {
		Temperature float64 `json:"temperature"`
	} `json:"generationConfig"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []GeminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// TranslateBatch 批量翻译歌词行，支持上下文提示、双路模型按优先级自动切换
// 返回：(翻译后的纯文本行切片, 实际成功的模型标识, error)
func (c *Client) TranslateBatch(ctx context.Context, cleanLines []string, sourceLang, targetLang string, contextInfo string, isRomanize bool) ([]string, string, error) {
	if len(cleanLines) == 0 {
		return []string{}, "", nil
	}

	priority := c.cfg.GetPriority()
	providers := c.cfg.GetProviders()

	var lastErr error
	var attemptedCount int

	for _, provName := range priority {
		prov, ok := providers[provName]
		if !ok {
			continue
		}

		// 检查密钥是否配置
		if strings.TrimSpace(prov.APIKey) == "" || strings.TrimSpace(prov.APIKey) == "your_deepseek_api_key" || strings.TrimSpace(prov.APIKey) == "your_gemini_api_key" {
			log.Printf("[INFO] 模型 %s 未配置有效 API Key，跳过该优先级", provName)
			continue
		}

		attemptedCount++
		log.Printf("[LLM] 正在通过优先级模型 [%s] (%s) 发起请求...", provName, prov.Model)

		resultLines, err := c.callProviderWithRetry(ctx, provName, prov, cleanLines, sourceLang, targetLang, contextInfo, isRomanize)
		if err == nil {
			return resultLines, fmt.Sprintf("%s (%s)", provName, prov.Model), nil
		}

		lastErr = err
		log.Printf("[WARN] 模型 [%s] 翻译失败: %v", provName, err)

		// 尝试下一个优先级提供商
		log.Printf("[FAILOVER] 正在自动故障切换至下一个优先级模型...")
	}

	if attemptedCount == 0 {
		return nil, "", fmt.Errorf("没有可用的模型配置（请在配置文件或后台网页中配置有效的 API Key）")
	}

	return nil, "", fmt.Errorf("所有优先级模型均调用失败，最后一次错误: %w", lastErr)
}

// callProviderWithRetry 针对单个提供商执行带重试的调用
func (c *Client) callProviderWithRetry(ctx context.Context, provName string, prov config.ProviderConfig, cleanLines []string, sourceLang, targetLang string, contextInfo string, isRomanize bool) ([]string, error) {
	maxRetries := prov.MaxRetries
	if maxRetries < 1 {
		maxRetries = 1
	}

	systemPrompt := BuildSystemPrompt(targetLang, c.cfg.GetUserPrompt(), c.cfg.GetMetaPrompt(), isRomanize)

	var lastErr error
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		userContent := BuildUserContent(cleanLines, contextInfo, attempt)

		var translatedRaw string
		var err error

		// 根据提供商协议类型分发
		isGeminiNative := strings.ToLower(prov.Type) == "gemini" ||
			(prov.Type == "" && strings.Contains(prov.BaseURL, "generativelanguage.googleapis.com"))

		if isGeminiNative {
			translatedRaw, err = c.callGeminiAPI(ctx, prov, systemPrompt, userContent)
		} else {
			translatedRaw, err = c.callOpenAIAPI(ctx, prov, systemPrompt, userContent)
		}

		if err != nil {
			lastErr = err
			log.Printf("[WARN] [%s] API 调用异常 (尝试 %d/%d): %v", provName, attempt, maxRetries+1, err)
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		// 结果清洗与行数校验
		resultLines := cleanOutputLines(translatedRaw)
		if len(resultLines) == len(cleanLines) {
			return resultLines, nil
		}

		log.Printf("[WARN] [%s] 返回行数不一致 (期望 %d 行，实际 %d 行)，准备重试或对齐", provName, len(cleanLines), len(resultLines))

		// 若已达最大重试次数，进行兜底行对齐
		if attempt == maxRetries+1 {
			aligned := alignLines(resultLines, cleanLines)
			return aligned, nil
		}
	}

	return nil, fmt.Errorf("provider %s failed after retries: %w", provName, lastErr)
}

// callOpenAIAPI 调用兼容 OpenAI 格式的 API (DeepSeek / SiliconFlow / 中转站等)
func (c *Client) callOpenAIAPI(ctx context.Context, prov config.ProviderConfig, systemPrompt, userPrompt string) (string, error) {
	reqURL := strings.TrimRight(prov.BaseURL, "/")
	if !strings.HasSuffix(reqURL, "/chat/completions") {
		reqURL += "/chat/completions"
	}

	temp := prov.Temperature
	if temp <= 0 {
		temp = 0.2
	}

	payload := ChatCompletionRequest{
		Model: prov.Model,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: temp,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request failed: %w", err)
	}

	reqTimeout := time.Duration(prov.TimeoutSeconds) * time.Second
	if reqTimeout <= 0 {
		reqTimeout = 60 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if prov.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+prov.APIKey)
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

// callGeminiAPI 调用 Google Gemini 官方 REST API (generateContent)
func (c *Client) callGeminiAPI(ctx context.Context, prov config.ProviderConfig, systemPrompt, userPrompt string) (string, error) {
	baseURL := strings.TrimRight(prov.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
	}

	model := prov.Model
	if model == "" {
		model = "gemini-3.5-flash"
	}

	reqURL := fmt.Sprintf("%s/models/%s:generateContent?key=%s", baseURL, model, prov.APIKey)

	temp := prov.Temperature
	if temp <= 0 {
		temp = 0.2
	}

	payload := GeminiRequest{
		SystemInstruction: &GeminiContent{
			Parts: []GeminiPart{{Text: systemPrompt}},
		},
		Contents: []GeminiContent{
			{
				Role:  "user",
				Parts: []GeminiPart{{Text: userPrompt}},
			},
		},
	}
	payload.GenerationConfig.Temperature = temp

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal gemini request failed: %w", err)
	}

	reqTimeout := time.Duration(prov.TimeoutSeconds) * time.Second
	if reqTimeout <= 0 {
		reqTimeout = 60 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, reqTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create gemini request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini http request error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read gemini response body error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini api responded with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return "", fmt.Errorf("unmarshal gemini response failed: %w", err)
	}

	if geminiResp.Error != nil && geminiResp.Error.Message != "" {
		return "", fmt.Errorf("gemini api error [%d %s]: %s", geminiResp.Error.Code, geminiResp.Error.Status, geminiResp.Error.Message)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty candidates returned from gemini")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
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

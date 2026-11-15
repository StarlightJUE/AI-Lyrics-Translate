package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

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

	// 非罗马音模式下，如果全曲仅包含符号/Emoji/空白（无任何可翻译语种字符），0 Token 直接返回全空行
	if !isRomanize && allLinesSymbolOnly(cleanLines) {
		return make([]string, len(cleanLines)), "local-skip (no-translatable-text)", nil
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
	var missingIndices []int
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		userContent := BuildUserContentWithMissing(cleanLines, contextInfo, attempt, missingIndices)

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

		// 智能解析输出（优先 JSON 行号映射，向后兼容纯文本格式）
		parsedLines, missing, isJSON := parseLLMOutput(translatedRaw, len(cleanLines))
		if len(missing) == 0 && len(parsedLines) == len(cleanLines) {
			return postProcessTranslations(parsedLines, cleanLines, isRomanize), nil
		}

		if isJSON && len(missing) > 0 {
			missingIndices = missing
			log.Printf("[WARN] [%s] 返回 JSON 缺少编号 %v (共 %d 行)，准备重试或对齐", provName, missing, len(cleanLines))
		} else {
			missingIndices = nil
			log.Printf("[WARN] [%s] 返回行数不一致 (期望 %d 行，实际 %d 行)，准备重试或对齐", provName, len(cleanLines), len(parsedLines))
		}

		// 若已达最大重试次数，进行安全兜底行对齐（保持已解析行号绝对不串位）
		if attempt == maxRetries+1 {
			aligned := alignLinesSafe(parsedLines, missing, isJSON, cleanLines, targetLang, isRomanize)
			return postProcessTranslations(aligned, cleanLines, isRomanize), nil
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

// stripMarkdownFence 剥离可能包裹在输出外层的 Markdown 代码块标记
func stripMarkdownFence(raw string) string {
	content := strings.TrimSpace(raw)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 2 {
			if strings.HasPrefix(lines[0], "```") {
				lines = lines[1:]
			}
			if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			content = strings.Join(lines, "\n")
		}
	}
	return strings.TrimSpace(content)
}

// sanitizeSingleLine 清理单行值，防止内部携带意外换行破坏行结构
func sanitizeSingleLine(val string) string {
	s := strings.ReplaceAll(val, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// parseLLMOutput 智能解析大模型返回结果，优先按行号 JSON 映射，向后兼容 JSON 数组与纯文本行
func parseLLMOutput(raw string, expectedCount int) ([]string, []int, bool) {
	content := stripMarkdownFence(raw)

	if lines, missing, ok := tryParseJSONObject(content, expectedCount); ok {
		return lines, missing, true
	}

	if lines, ok := tryParseJSONArray(content, expectedCount); ok {
		return lines, nil, true
	}

	textLines := cleanOutputLines(content, expectedCount)
	return textLines, nil, false
}

func tryParseJSONObject(content string, expectedCount int) ([]string, []int, bool) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end <= start {
		return nil, nil, false
	}

	jsonSub := content[start : end+1]
	var rawMap map[string]any
	if err := json.Unmarshal([]byte(jsonSub), &rawMap); err != nil {
		return nil, nil, false
	}

	idxMap := make(map[int]string, len(rawMap))
	matchedCount := 0
	for k, v := range rawMap {
		idx, err := strconv.Atoi(strings.TrimSpace(k))
		if err != nil || idx < 0 || idx >= expectedCount {
			continue
		}
		matchedCount++
		if v == nil {
			idxMap[idx] = ""
		} else {
			idxMap[idx] = sanitizeSingleLine(fmt.Sprintf("%v", v))
		}
	}

	// 必须至少命中一个有效下标键才认定为行号 JSON 映射
	if matchedCount == 0 {
		return nil, nil, false
	}

	result := make([]string, expectedCount)
	var missing []int
	for i := 0; i < expectedCount; i++ {
		if val, exists := idxMap[i]; exists {
			result[i] = val
		} else {
			missing = append(missing, i)
		}
	}
	return result, missing, true
}

func tryParseJSONArray(content string, expectedCount int) ([]string, bool) {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start == -1 || end <= start {
		return nil, false
	}

	var arr []any
	if err := json.Unmarshal([]byte(content[start:end+1]), &arr); err != nil {
		return nil, false
	}
	if len(arr) != expectedCount {
		return nil, false
	}

	result := make([]string, expectedCount)
	for i, v := range arr {
		if v == nil {
			result[i] = ""
		} else {
			result[i] = sanitizeSingleLine(fmt.Sprintf("%v", v))
		}
	}
	return result, true
}

// cleanOutputLines 清理纯文本换行响应，小心保留应有的空行直到对齐预期行数
func cleanOutputLines(content string, expectedCount int) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	rawLines := strings.Split(content, "\n")
	result := make([]string, len(rawLines))
	for i, l := range rawLines {
		result[i] = strings.TrimSpace(l)
	}

	// 仅当多出末尾空行时才逐行裁剪，防止误删末尾本身为空的合法译文行
	for len(result) > expectedCount && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}

	return result
}

// alignLinesSafe 安全兜底对齐：JSON 映射模式下已对齐的编号绝不错位，仅填充真正缺失的编号
func alignLinesSafe(parsed []string, missing []int, isJSON bool, original []string, targetLang string, isRomanize bool) []string {
	targetLen := len(original)
	if isJSON {
		aligned := make([]string, targetLen)
		copy(aligned, parsed)
		for _, idx := range missing {
			if idx < 0 || idx >= targetLen {
				continue
			}
			if !isRomanize && isLikelyTargetLangFallback(original[idx], targetLang) {
				aligned[idx] = ""
			} else {
				aligned[idx] = original[idx]
			}
		}
		return aligned
	}

	aligned := make([]string, targetLen)
	for i := 0; i < targetLen; i++ {
		if i < len(parsed) {
			aligned[i] = parsed[i]
		} else if !isRomanize && isLikelyTargetLangFallback(original[i], targetLang) {
			aligned[i] = ""
		} else {
			aligned[i] = original[i]
		}
	}
	return aligned
}

// postProcessTranslations 后处理：常规翻译模式下，若内容未变或为纯符号行则置为空行 ""
func postProcessTranslations(results []string, originals []string, isRomanize bool) []string {
	out := make([]string, len(results))
	for i, res := range results {
		cleaned := sanitizeSingleLine(res)
		if isRomanize {
			out[i] = cleaned
			continue
		}
		if cleaned == "" {
			out[i] = ""
			continue
		}
		if i < len(originals) && (isBlankOrSymbolOnly(originals[i]) || isSameContent(originals[i], cleaned)) {
			out[i] = ""
			continue
		}
		out[i] = cleaned
	}
	return out
}

func allLinesSymbolOnly(lines []string) bool {
	for _, l := range lines {
		if !isBlankOrSymbolOnly(l) {
			return false
		}
	}
	return true
}

func isBlankOrSymbolOnly(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return false
		}
	}
	return true
}

func stripPunctAndSpace(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}

func isSameContent(orig, trans string) bool {
	if strings.EqualFold(strings.TrimSpace(orig), strings.TrimSpace(trans)) {
		return true
	}
	noPunctOrig := stripPunctAndSpace(orig)
	noPunctTrans := stripPunctAndSpace(trans)
	return noPunctOrig != "" && noPunctOrig == noPunctTrans
}

func isLikelyTargetLangFallback(line, targetLang string) bool {
	if isBlankOrSymbolOnly(line) {
		return true
	}
	t := strings.ToLower(strings.TrimSpace(targetLang))
	isZhTarget := t == "zh" || t == "zh-hans" || t == "zh-hant" || t == "zh-cn" || t == "zh-tw" || t == "zt" || t == "simplified" || t == "traditional"
	if isZhTarget {
		var hasHan, hasKanaOrHangul, hasLatinWord bool
		latinSeq := 0
		for _, r := range line {
			switch {
			case unicode.Is(unicode.Han, r):
				hasHan = true
				latinSeq = 0
			case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
				hasKanaOrHangul = true
				latinSeq = 0
			case unicode.IsLetter(r):
				latinSeq++
				if latinSeq >= 6 {
					hasLatinWord = true
				}
			default:
				latinSeq = 0
			}
		}
		return hasHan && !hasKanaOrHangul && !hasLatinWord
	}
	if t == "en" {
		for _, r := range line {
			if unicode.IsLetter(r) && r > unicode.MaxASCII {
				return false
			}
		}
		return true
	}
	return false
}

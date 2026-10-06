package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ai-lyrics-translate/config"
	"ai-lyrics-translate/llm"
	"ai-lyrics-translate/normalizer"
	"ai-lyrics-translate/storage"
)

// Server LibreTranslate 兼容服务
type Server struct {
	cfg      *config.Config
	storage  *storage.Storage
	llm      *llm.Client
	inflight *InFlightManager
}

// NewServer 构造函数
func NewServer(cfg *config.Config, store *storage.Storage, llmClient *llm.Client) *Server {
	return &Server{
		cfg:      cfg,
		storage:  store,
		llm:      llmClient,
		inflight: NewInFlightManager(),
	}
}

// Close 优雅停机，等待未完成的后台翻译任务落盘
func (s *Server) Close() {
	if s.inflight != nil {
		s.inflight.WaitAll(5 * time.Second)
	}
}

// TranslateRequest 兼容 LibreTranslate 请求参数
type TranslateRequest struct {
	Q      any    `json:"q"`      // string 或 []string
	Source string `json:"source"` // 源语言，默认 auto
	Target string `json:"target"` // 目标语言，默认 zh
	Format string `json:"format"` // text 或 html
	APIKey string `json:"api_key"`
}

// TranslateResponseSingle 兼容单字符串返回格式
type TranslateResponseSingle struct {
	TranslatedText   string `json:"translatedText"`
	DetectedLanguage *struct {
		Confidence float64 `json:"confidence"`
		Language   string  `json:"language"`
	} `json:"detectedLanguage,omitempty"`
}

// TranslateResponseBatch 兼容数组返回格式
type TranslateResponseBatch struct {
	TranslatedText []string `json:"translatedText"`
}

// LanguageItem LibreTranslate 语言定义
type LanguageItem struct {
	Code    string   `json:"code"`
	Name    string   `json:"name"`
	Targets []string `json:"targets"`
}

// RegisterRoutes 注册 HTTP 路由并集成 CORS 中间件
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/translate", s.corsMiddleware(s.handleTranslate))
	mux.HandleFunc("/languages", s.corsMiddleware(s.handleLanguages))
	mux.HandleFunc("/detect", s.corsMiddleware(s.handleDetect))
	mux.HandleFunc("/frontend/settings", s.corsMiddleware(s.handleFrontendSettings))
	mux.HandleFunc("/health", s.corsMiddleware(s.handleHealth))
	mux.HandleFunc("/", s.corsMiddleware(s.handleIndex))
}

func (s *Server) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":     "AI-Lyrics-Translate",
		"version":     "1.0.0",
		"protocol":    "LibreTranslate Standard",
		"status":      "running",
		"model":       s.cfg.LLMModel,
		"prompt_ver":  s.cfg.PromptVersion,
		"caching":     "SQLite WAL (SHA-256 Content-Aware)",
		"currentTime": time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	languages := []LanguageItem{
		{Code: "zh", Name: "Chinese (Simplified)", Targets: []string{"en", "ja", "ko", "zt", "fr", "de", "es", "ru"}},
		{Code: "zt", Name: "Chinese (Traditional)", Targets: []string{"en", "ja", "ko", "zh", "fr", "de", "es", "ru"}},
		{Code: "en", Name: "English", Targets: []string{"zh", "zt", "ja", "ko", "fr", "de", "es", "ru"}},
		{Code: "ja", Name: "Japanese", Targets: []string{"zh", "zt", "en", "ko"}},
		{Code: "ko", Name: "Korean", Targets: []string{"zh", "zt", "en", "ja"}},
		{Code: "auto", Name: "Auto Detect", Targets: []string{"zh", "zt", "en", "ja", "ko"}},
	}
	writeJSON(w, http.StatusOK, languages)
}

func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]any{
		{"confidence": 99.0, "language": "en"},
	})
}

func (s *Server) handleFrontendSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"apiKeys":              false,
		"charLimit":            -1,
		"frontendTimeout":      500,
		"keyRequired":          false,
		"language":             map[string]any{"source": map[string]string{"code": "auto", "name": "Auto Detect"}, "target": map[string]string{"code": "zh", "name": "Chinese"}},
		"suggestions":          false,
		"supportedFilesFormat": []string{},
	})
}

// handleTranslate 处理核心翻译请求
func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TranslateRequest

	// 同时兼容 application/json 与 表单提交
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Read body failed: %v", err), http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}
	} else {
		// Form 表单格式支持（处理 application/x-www-form-urlencoded）
		if err := r.ParseForm(); err != nil {
			http.Error(w, fmt.Sprintf("Parse form error: %v", err), http.StatusBadRequest)
			return
		}
		qValues := r.Form["q"]
		if len(qValues) == 1 {
			req.Q = qValues[0]
		} else if len(qValues) > 1 {
			req.Q = qValues
		} else {
			req.Q = r.FormValue("q")
		}
		req.Source = r.FormValue("source")
		req.Target = r.FormValue("target")
		req.Format = r.FormValue("format")
	}

	// 目标语言解析：根据客户端 target 与 env 默认配置做精准简繁体或通用语言映射
	resolvedTarget := s.cfg.ResolveTargetLanguage(req.Target)

	// 源语言设定：无论客户端传来何种 source（即使误判为 en），后续 LLM 均被提示词约束为自主识别源语言
	sourceLang := req.Source
	if sourceLang == "" {
		sourceLang = "auto"
	}

	ctx := r.Context()

	// 分支 1：q 为单个字符串（整首歌词全文或单句）
	if singleStr, ok := req.Q.(string); ok {
		translated, err := s.translateSingleText(ctx, singleStr, sourceLang, resolvedTarget)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// 客户端断开连接/切歌，大模型已在独立后台继续执行并落盘，无需报错
				return
			}
			log.Printf("[ERROR] Translate error: %v", err)
			http.Error(w, fmt.Sprintf("Translation failed: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, TranslateResponseSingle{
			TranslatedText: translated,
		})
		return
	}

	// 分支 2：q 为 []any
	if slice, ok := req.Q.([]any); ok {
		var results []string
		for _, item := range slice {
			if ctx.Err() != nil {
				return
			}
			strVal := fmt.Sprintf("%v", item)
			translated, err := s.translateSingleText(ctx, strVal, sourceLang, resolvedTarget)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Printf("[ERROR] Translate slice item error: %v", err)
				results = append(results, strVal) // 错误降级保留原文
			} else {
				results = append(results, translated)
			}
		}
		writeJSON(w, http.StatusOK, TranslateResponseBatch{
			TranslatedText: results,
		})
		return
	}

	// 分支 3：q 为 []string
	if slice, ok := req.Q.([]string); ok {
		var results []string
		for _, strVal := range slice {
			if ctx.Err() != nil {
				return
			}
			translated, err := s.translateSingleText(ctx, strVal, sourceLang, resolvedTarget)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Printf("[ERROR] Translate slice item error: %v", err)
				results = append(results, strVal)
			} else {
				results = append(results, translated)
			}
		}
		writeJSON(w, http.StatusOK, TranslateResponseBatch{
			TranslatedText: results,
		})
		return
	}

	http.Error(w, "Field 'q' is required and must be string or array", http.StatusBadRequest)
}

// translateSingleText 单篇歌词/文本的完整翻译生命周期（哈希计算 -> 查缓存 -> LLM翻译 -> 写缓存 -> 时间轴重组）
func (s *Server) translateSingleText(ctx context.Context, rawText, sourceLang, targetLang string) (string, error) {
	if strings.TrimSpace(rawText) == "" {
		return rawText, nil
	}

	startTime := time.Now()

	// 1. 生成内容感知哈希键与标准化文本
	cacheKey, normalizedText := normalizer.GenerateCacheKey(rawText, targetLang)

	// 解析原歌词结构（提取时间轴与纯歌词行）
	parsed := normalizer.ParseLyrics(rawText)

	// 2. 检查本地 SQLite 缓存
	cached, err := s.storage.Get(cacheKey)
	if err != nil {
		log.Printf("[WARN] Failed to query cache for key %s: %v", cacheKey[:8], err)
	}

	if cached != nil {
		// 缓存命中！
		duration := time.Since(startTime)
		log.Printf("[CACHE HIT] Key=%s... | 耗时: %v | 0 API Token 消耗", cacheKey[:12], duration)

		cachedLines := strings.Split(cached.TranslatedText, "\n")
		return parsed.RebuildLyrics(cachedLines), nil
	}

	// 3. 缓存未命中：提取格式化后的纯歌词行
	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) == 0 {
		return rawText, nil
	}

	log.Printf("[CACHE MISS] Key=%s... | 纯歌词行数: %d | 正在请求模型: %s", cacheKey[:12], len(cleanLines), s.cfg.LLMModel)

	// 计算超时时间（包含重试冗余与网络缓冲）
	timeout := time.Duration(s.cfg.LLMTimeoutSeconds*(s.cfg.LLMMaxRetries+1)+15) * time.Second
	if timeout < 60*time.Second {
		timeout = 60 * time.Second
	}

	// 4. 调用并发协调器：并发去重并在脱离客户端生命周期的独立后台 Goroutine 中调用大模型与写缓存
	translatedCleanLines, err := s.inflight.ExecuteOrAttach(ctx, cacheKey, timeout, func(bgCtx context.Context) ([]string, error) {
		bgStart := time.Now()

		// 4.1 调用大模型批量翻译
		lines, err := s.llm.TranslateBatch(bgCtx, cleanLines, sourceLang, targetLang)
		if err != nil {
			return nil, fmt.Errorf("llm batch translation failed: %w", err)
		}

		// 4.2 写入 SQLite 本地缓存并落盘
		cacheEntry := &storage.CachedTranslation{
			CacheKey:       cacheKey,
			SourceText:     normalizedText,
			TranslatedText: strings.Join(lines, "\n"),
			SourceLang:     sourceLang,
			TargetLang:     targetLang,
			ModelName:      s.cfg.LLMModel,
			PromptVersion:  s.cfg.PromptVersion,
		}

		if err := s.storage.Set(cacheEntry); err != nil {
			log.Printf("[WARN] Failed to save translation to cache: %v", err)
		} else {
			log.Printf("[CACHE SAVED] Key=%s... 已成功缓存至 SQLite (耗时: %v)", cacheKey[:12], time.Since(bgStart))
		}

		return lines, nil
	})

	if err != nil {
		return "", err
	}

	// 5. 重组时间轴与元数据并返回
	duration := time.Since(startTime)
	log.Printf("[TRANSLATE SUCCESS] Key=%s... | 总耗时: %v", cacheKey[:12], duration)

	return parsed.RebuildLyrics(translatedCleanLines), nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[ERROR] JSON encode error: %v", err)
	}
}

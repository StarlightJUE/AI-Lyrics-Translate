package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-lyrics-translate/config"
	"ai-lyrics-translate/llm"
	"ai-lyrics-translate/normalizer"
	"ai-lyrics-translate/storage"
)

// Version 服务版本号
const Version = "1.1.0"

//go:embed web/index.html
var indexHTML []byte

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

// TranslateRequest 兼容 LibreTranslate 请求参数，扩展罗马音字段
type TranslateRequest struct {
	Q             any    `json:"q"`                        // string 或 []string
	Source        string `json:"source"`                   // 源语言，默认 auto
	Target        string `json:"target"`                   // 目标语言，默认 zh
	Format        string `json:"format"`                   // text 或 html
	APIKey        string `json:"api_key"`
	Romaji        *bool  `json:"romaji,omitempty"`        // 罗马音转换标识
	Romanize      *bool  `json:"romanize,omitempty"`      // 罗马音转换标识
	Transliterate *bool  `json:"transliterate,omitempty"` // 音译转换标识
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

// RegisterBusinessRoutes 仅注册 LibreTranslate 核心业务接口
func (s *Server) RegisterBusinessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/translate", s.corsMiddleware(s.handleTranslate))
	mux.HandleFunc("/languages", s.corsMiddleware(s.handleLanguages))
	mux.HandleFunc("/detect", s.corsMiddleware(s.handleDetect))
	mux.HandleFunc("/frontend/settings", s.corsMiddleware(s.handleFrontendSettings))
	mux.HandleFunc("/health", s.corsMiddleware(s.handleHealth))
}

// RegisterAdminRoutes 仅注册管理后台与控制台相关路由（独立管理端口模式）
func (s *Server) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/lyrics", s.corsMiddleware(s.handleAPILyrics))
	mux.HandleFunc("/api/config", s.corsMiddleware(s.handleAPIConfig))
	mux.HandleFunc("/ui", s.corsMiddleware(s.handleUI))
	mux.HandleFunc("/dashboard", s.corsMiddleware(s.handleUI))
	mux.HandleFunc("/health", s.corsMiddleware(s.handleHealth))
	mux.HandleFunc("/", s.corsMiddleware(s.handleIndex))
}

// RegisterRoutes 注册全部 HTTP 路由（业务端与管理端合一模式）
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	s.RegisterBusinessRoutes(mux)

	// Web 控制台与 REST 管理 API
	mux.HandleFunc("/api/lyrics", s.corsMiddleware(s.handleAPILyrics))
	mux.HandleFunc("/api/config", s.corsMiddleware(s.handleAPIConfig))
	mux.HandleFunc("/ui", s.corsMiddleware(s.handleUI))
	mux.HandleFunc("/dashboard", s.corsMiddleware(s.handleUI))
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

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// 若客户端是浏览器请求（Accept 包含 text/html），直接展示单页面管理前端
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "text/html") {
		s.handleUI(w, r)
		return
	}

	// 否则返回兼容的 JSON 运行状态
	pri := s.cfg.GetPriority()
	provs := s.cfg.GetProviders()
	var activeModel string
	if len(pri) > 0 {
		activeModel = fmt.Sprintf("%s (%s)", pri[0], provs[pri[0]].Model)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"service":     "AI-Lyrics-Translate",
		"version":     Version,
		"protocol":    "LibreTranslate Standard",
		"status":      "running",
		"model":       activeModel,
		"priority":    pri,
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
		{Code: "zh", Name: "Chinese (Simplified)", Targets: []string{"en", "ja", "ko", "zt", "fr", "de", "es", "ru", "romaji"}},
		{Code: "zt", Name: "Chinese (Traditional)", Targets: []string{"en", "ja", "ko", "zh", "fr", "de", "es", "ru", "romaji"}},
		{Code: "en", Name: "English", Targets: []string{"zh", "zt", "ja", "ko", "fr", "de", "es", "ru", "romaji"}},
		{Code: "ja", Name: "Japanese", Targets: []string{"zh", "zt", "en", "ko", "romaji"}},
		{Code: "ko", Name: "Korean", Targets: []string{"zh", "zt", "en", "ja", "romaji"}},
		{Code: "romaji", Name: "Romanization (日韩罗马音)", Targets: []string{"zh", "en"}},
		{Code: "auto", Name: "Auto Detect", Targets: []string{"zh", "zt", "en", "ja", "ko", "romaji"}},
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

// handleAPILyrics 歌词缓存管理接口 (GET 查询列表 / PUT 修改歌词 / DELETE 删除缓存)
func (s *Server) handleAPILyrics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 50
		offset := 0
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
				limit = l
			}
		}
		if oStr := r.URL.Query().Get("offset"); oStr != "" {
			if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
				offset = o
			}
		}
		q := strings.TrimSpace(r.URL.Query().Get("q"))

		items, total, err := s.storage.List(limit, offset, q)
		if err != nil {
			http.Error(w, fmt.Sprintf("List error: %v", err), http.StatusInternalServerError)
			return
		}

		type LyricItemDTO struct {
			CacheKey       string `json:"cache_key"`
			SourceText     string `json:"source_text"`
			TranslatedText string `json:"translated_text"`
			SourceLang     string `json:"source_lang"`
			TargetLang     string `json:"target_lang"`
			ModelName      string `json:"model_name"`
			PromptVersion  string `json:"prompt_version"`
			UpdatedAt      string `json:"updated_at"`
		}

		var dtos []LyricItemDTO
		for _, item := range items {
			dtos = append(dtos, LyricItemDTO{
				CacheKey:       item.CacheKey,
				SourceText:     item.SourceText,
				TranslatedText: item.TranslatedText,
				SourceLang:     item.SourceLang,
				TargetLang:     item.TargetLang,
				ModelName:      item.ModelName,
				PromptVersion:  item.PromptVersion,
				UpdatedAt:      item.UpdatedAt.Format(time.RFC3339),
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"total": total,
			"items": dtos,
		})

	case http.MethodPut:
		var payload struct {
			Key            string `json:"key"`
			TranslatedText string `json:"translated_text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		if payload.Key == "" {
			http.Error(w, "Key is required", http.StatusBadRequest)
			return
		}
		if err := s.storage.UpdateTranslation(payload.Key, payload.TranslatedText); err != nil {
			http.Error(w, fmt.Sprintf("Update failed: %v", err), http.StatusInternalServerError)
			return
		}
		log.Printf("[WEB UI] 手动修改歌词缓存成功: Key=%s...", payload.Key[:8])
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})

	case http.MethodDelete:
		key := strings.TrimSpace(r.URL.Query().Get("key"))
		if key == "" {
			http.Error(w, "Query param 'key' is required", http.StatusBadRequest)
			return
		}
		if key == "all" {
			if err := s.storage.DeleteAll(); err != nil {
				http.Error(w, fmt.Sprintf("Delete all failed: %v", err), http.StatusInternalServerError)
				return
			}
			log.Printf("[WEB UI] 已清空全部歌词缓存！")
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		if err := s.storage.Delete(key); err != nil {
			http.Error(w, fmt.Sprintf("Delete failed: %v", err), http.StatusInternalServerError)
			return
		}
		log.Printf("[WEB UI] 删除歌词缓存成功: Key=%s...", key[:8])
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleAPIConfig 配置查看与更新接口 (GET 查询配置 / POST 更新配置)
func (s *Server) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pri := s.cfg.GetPriority()
		provs := s.cfg.GetProviders()

		type ProviderDTO struct {
			Type           string  `json:"type"`
			BaseURL        string  `json:"base_url"`
			Model          string  `json:"model"`
			HasKey         bool    `json:"has_key"`
			KeyMasked      string  `json:"key_masked"`
			Temperature    float64 `json:"temperature"`
			TimeoutSeconds int     `json:"timeout_seconds"`
			MaxRetries     int     `json:"max_retries"`
		}

		provDTOs := make(map[string]ProviderDTO)
		for name, p := range provs {
			hasKey := strings.TrimSpace(p.APIKey) != "" && strings.TrimSpace(p.APIKey) != "your_deepseek_api_key" && strings.TrimSpace(p.APIKey) != "your_gemini_api_key"
			provDTOs[name] = ProviderDTO{
				Type:           p.Type,
				BaseURL:        p.BaseURL,
				Model:          p.Model,
				HasKey:         hasKey,
				KeyMasked:      maskAPIKey(p.APIKey),
				Temperature:    p.Temperature,
				TimeoutSeconds: p.TimeoutSeconds,
				MaxRetries:     p.MaxRetries,
			}
		}

		metaPrompt := s.cfg.GetMetaPrompt()
		isCustomMeta := metaPrompt != ""
		if metaPrompt == "" {
			metaPrompt = llm.MetaPromptFramework
		}

		userPrompt := s.cfg.GetUserPrompt()
		isCustomUser := userPrompt != ""
		if userPrompt == "" {
			userPrompt = llm.GetDefaultUserPrompt("zh-Hans")
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"version":             Version,
			"priority":            pri,
			"admin_port":          s.cfg.GetAdminPort(),
			"providers":           provDTOs,
			"meta_prompt":         metaPrompt,
			"user_prompt":         userPrompt,
			"is_custom_meta":      isCustomMeta,
			"is_custom_user":      isCustomUser,
			"default_meta_prompt": llm.MetaPromptFramework,
			"default_user_prompt": llm.GetDefaultUserPrompt("zh-Hans"),
		})

	case http.MethodPost:
		var req struct {
			UserPrompt *string                          `json:"user_prompt,omitempty"`
			MetaPrompt *string                          `json:"meta_prompt,omitempty"`
			AdminPort  *string                          `json:"admin_port,omitempty"`
			Priority   []string                         `json:"priority,omitempty"`
			Providers  map[string]config.ProviderConfig `json:"providers,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if req.UserPrompt != nil {
			if err := s.cfg.UpdateUserPromptAndSave(*req.UserPrompt); err != nil {
				http.Error(w, fmt.Sprintf("Save prompt error: %v", err), http.StatusInternalServerError)
				return
			}
			log.Printf("[WEB UI] 用户提示词更新成功并已保存至配置文件")
		}

		if req.MetaPrompt != nil {
			if err := s.cfg.UpdateMetaPromptAndSave(*req.MetaPrompt); err != nil {
				http.Error(w, fmt.Sprintf("Save meta prompt error: %v", err), http.StatusInternalServerError)
				return
			}
			log.Printf("[WEB UI] 元提示词更新成功并已保存至配置文件")
		}

		if req.AdminPort != nil {
			if err := s.cfg.UpdateAdminPortAndSave(*req.AdminPort); err != nil {
				http.Error(w, fmt.Sprintf("Save admin port error: %v", err), http.StatusInternalServerError)
				return
			}
			log.Printf("[WEB UI] 管理端口更新成功并已保存至配置文件: %s", *req.AdminPort)
		}

		if len(req.Priority) > 0 {
			if err := s.cfg.UpdatePriorityAndSave(req.Priority); err != nil {
				http.Error(w, fmt.Sprintf("Save priority error: %v", err), http.StatusInternalServerError)
				return
			}
			log.Printf("[WEB UI] 模型优先级更新成功并已保存: %v", req.Priority)
		}

		if len(req.Providers) > 0 {
			for name, prov := range req.Providers {
				_ = s.cfg.UpdateProviderAndSave(name, prov)
			}
			log.Printf("[WEB UI] 模型提供商配置更新成功并已保存")
		}

		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
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

	// 判断客户端是否要求转罗马音
	isRomanize := s.checkRomanizeRequest(r, &req)

	// 目标语言解析：根据客户端 target 与配置做精准映射
	resolvedTarget := s.cfg.ResolveTargetLanguage(req.Target)
	if isRomanize {
		resolvedTarget = "romaji"
	}

	sourceLang := req.Source
	if sourceLang == "" {
		sourceLang = "auto"
	}

	ctx := r.Context()

	// 分支 1：q 为单个字符串（整首歌词全文或单句）
	if singleStr, ok := req.Q.(string); ok {
		translated, err := s.translateSingleText(ctx, singleStr, sourceLang, resolvedTarget, isRomanize)
		if err != nil {
			if errors.Is(err, context.Canceled) {
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
			translated, err := s.translateSingleText(ctx, strVal, sourceLang, resolvedTarget, isRomanize)
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

	// 分支 3：q 为 []string
	if slice, ok := req.Q.([]string); ok {
		var results []string
		for _, strVal := range slice {
			if ctx.Err() != nil {
				return
			}
			translated, err := s.translateSingleText(ctx, strVal, sourceLang, resolvedTarget, isRomanize)
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

// checkRomanizeRequest 检查客户端是否要求日韩语转罗马音
func (s *Server) checkRomanizeRequest(r *http.Request, req *TranslateRequest) bool {
	// 1. URL Query 参数
	q := r.URL.Query()
	for _, param := range []string{"romaji", "romanize", "transliterate", "to_romaji"} {
		val := strings.ToLower(strings.TrimSpace(q.Get(param)))
		if val == "true" || val == "1" || val == "yes" {
			return true
		}
	}
	if strings.ToLower(strings.TrimSpace(q.Get("target"))) == "romaji" {
		return true
	}

	// 2. Request body 字段
	if req != nil {
		if req.Romaji != nil && *req.Romaji {
			return true
		}
		if req.Romanize != nil && *req.Romanize {
			return true
		}
		if req.Transliterate != nil && *req.Transliterate {
			return true
		}
		t := strings.ToLower(strings.TrimSpace(req.Target))
		if t == "romaji" || t == "ja-latn" || t == "ko-latn" || t == "ja-romaji" || t == "ko-romaji" || t == "latn" || t == "roma" {
			return true
		}
	}

	// 3. HTTP Header
	for _, h := range []string{"X-Lyrics-Romanize", "X-Romaji", "X-Transliterate"} {
		val := strings.ToLower(strings.TrimSpace(r.Header.Get(h)))
		if val == "true" || val == "1" || val == "yes" {
			return true
		}
	}

	return false
}

// translateSingleText 单篇歌词/文本的完整翻译生命周期（带歌名歌手上下文提示、日韩罗马音支持与双路模型切换）
func (s *Server) translateSingleText(ctx context.Context, rawText, sourceLang, targetLang string, isRomanize bool) (string, error) {
	if strings.TrimSpace(rawText) == "" {
		return rawText, nil
	}

	startTime := time.Now()

	// 1. 缓存隔离：转罗马音时以 "romaji" 作为目标语言独立哈希
	cacheTarget := targetLang
	if isRomanize {
		cacheTarget = "romaji"
	}

	// 生成内容感知哈希键与标准化文本
	cacheKey, normalizedText := normalizer.GenerateCacheKey(rawText, cacheTarget)

	// 解析原歌词结构（提取时间轴、纯歌词行，并自动提取歌名与歌手背景信息）
	parsed := normalizer.ParseLyrics(rawText)
	contextInfo := parsed.FormatContextInfo()

	// 2. 检查本地 SQLite 缓存
	cached, err := s.storage.Get(cacheKey)
	if err != nil {
		log.Printf("[WARN] Failed to query cache for key %s: %v", cacheKey[:8], err)
	}

	if cached != nil {
		// 缓存命中！
		duration := time.Since(startTime)
		log.Printf("[CACHE HIT] Key=%s... | 耗时: %v | 0 API Token 消耗 (模型: %s)", cacheKey[:12], duration, cached.ModelName)

		cachedLines := strings.Split(cached.TranslatedText, "\n")
		return parsed.RebuildLyrics(cachedLines), nil
	}

	// 3. 缓存未命中：提取纯歌词行
	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) == 0 {
		return rawText, nil
	}

	modeDesc := "翻译"
	if isRomanize {
		modeDesc = "日韩罗马音转写"
	}
	if contextInfo != "" {
		log.Printf("[CACHE MISS] Key=%s... | 行数: %d | 模式: %s | %s", cacheKey[:12], len(cleanLines), modeDesc, contextInfo)
	} else {
		log.Printf("[CACHE MISS] Key=%s... | 行数: %d | 模式: %s", cacheKey[:12], len(cleanLines), modeDesc)
	}

	timeout := 120 * time.Second

	// 4. 调用并发协调器：后台执行并写入缓存
	translatedCleanLines, err := s.inflight.ExecuteOrAttach(ctx, cacheKey, timeout, func(bgCtx context.Context) ([]string, error) {
		bgStart := time.Now()

		// 4.1 调用大模型批量处理（附带背景信息、罗马音标识与双路切换）
		lines, modelUsed, err := s.llm.TranslateBatch(bgCtx, cleanLines, sourceLang, targetLang, contextInfo, isRomanize)
		if err != nil {
			return nil, fmt.Errorf("llm batch translation failed: %w", err)
		}

		// 4.2 写入 SQLite 本地缓存并落盘
		cacheEntry := &storage.CachedTranslation{
			CacheKey:       cacheKey,
			SourceText:     normalizedText,
			TranslatedText: strings.Join(lines, "\n"),
			SourceLang:     sourceLang,
			TargetLang:     cacheTarget,
			ModelName:      modelUsed,
			PromptVersion:  s.cfg.PromptVersion,
		}

		if err := s.storage.Set(cacheEntry); err != nil {
			log.Printf("[WARN] Failed to save translation to cache: %v", err)
		} else {
			log.Printf("[CACHE SAVED] Key=%s... 已成功缓存至 SQLite (耗时: %v, 模型: %s)", cacheKey[:12], time.Since(bgStart), modelUsed)
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

func maskAPIKey(key string) string {
	k := strings.TrimSpace(key)
	if len(k) <= 6 {
		return "******"
	}
	return k[:6] + "..."
}

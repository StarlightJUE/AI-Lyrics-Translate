package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ai-lyrics-translate/config"
	"ai-lyrics-translate/llm"
	"ai-lyrics-translate/normalizer"
	"ai-lyrics-translate/storage"
)

func setupTestServer(t *testing.T) (*Server, *storage.Storage, func()) {
	tmpDB := "test_cache.db"
	store, err := storage.InitDB(tmpDB)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	cfg := &config.Config{
		ServerPort:     "5000",
		ServerHost:     "127.0.0.1",
		LLMPriority:    []string{"deepseek"},
		LLMProviders: map[string]config.ProviderConfig{
			"deepseek": {
				Type:        "openai",
				BaseURL:     "https://api.deepseek.com",
				Model:       "deepseek-flash",
				Temperature: 0.2,
			},
		},
		PromptVersion:  "v1.0",
		LLMTemperature: 0.2,
	}

	llmClient := llm.NewClient(cfg)
	srv := NewServer(cfg, store, llmClient)

	cleanup := func() {
		store.Close()
		os.Remove(tmpDB)
		os.Remove(tmpDB + "-wal")
		os.Remove(tmpDB + "-shm")
	}

	return srv, store, cleanup
}

func TestLibreTranslateLanguagesEndpoint(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/languages", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w.Code)
	}

	var langs []LanguageItem
	if err := json.Unmarshal(w.Body.Bytes(), &langs); err != nil {
		t.Fatalf("failed to parse languages json: %v", err)
	}

	if len(langs) == 0 {
		t.Errorf("languages list is empty")
	}
}

func TestCacheHitAndTimelineRebuilding(t *testing.T) {
	srv, store, cleanup := setupTestServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	rawLrc := `[ti:Test Song]
[00:01.00]When I was young
[00:05.00]I listened to the radio`

	// 1. 预先向 SQLite 写入该歌词的翻译缓存
	targetLang := srv.cfg.ResolveTargetLanguage("zh")
	cacheKey, normText := normalizer.GenerateCacheKey(rawLrc, targetLang)
	translatedClean := "当我年少时\n我常常倾听收音机"

	err := store.Set(&storage.CachedTranslation{
		CacheKey:       cacheKey,
		SourceText:     normText,
		TranslatedText: translatedClean,
		SourceLang:     "en",
		TargetLang:     targetLang,
		ModelName:      "deepseek-flash",
		PromptVersion:  "v1.0",
	})
	if err != nil {
		t.Fatalf("failed to seed cache: %v", err)
	}

	// 2. 发起标准 LibreTranslate POST /translate 请求
	payload := map[string]any{
		"q":      rawLrc,
		"source": "auto",
		"target": "zh",
		"format": "text",
	}
	bodyBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/translate", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp TranslateResponseSingle
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// 3. 验证结果包含原时间戳和翻译文本
	if !strings.Contains(resp.TranslatedText, "[ti:Test Song]") {
		t.Errorf("metadata missing in response: %s", resp.TranslatedText)
	}
	if !strings.Contains(resp.TranslatedText, "[00:01.00] 当我年少时") {
		t.Errorf("expected [00:01.00] 当我年少时, got %s", resp.TranslatedText)
	}
	if !strings.Contains(resp.TranslatedText, "[00:05.00] 我常常倾听收音机") {
		t.Errorf("expected [00:05.00] 我常常倾听收音机, got %s", resp.TranslatedText)
	}
}

func TestBatchTranslateArrayFormat(t *testing.T) {
	srv, store, cleanup := setupTestServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	targetLang := srv.cfg.ResolveTargetLanguage("zh")

	// 预置缓存
	k1, n1 := normalizer.GenerateCacheKey("Hello", targetLang)
	_ = store.Set(&storage.CachedTranslation{
		CacheKey:       k1,
		SourceText:     n1,
		TranslatedText: "你好",
		TargetLang:     targetLang,
	})

	k2, n2 := normalizer.GenerateCacheKey("World", targetLang)
	_ = store.Set(&storage.CachedTranslation{
		CacheKey:       k2,
		SourceText:     n2,
		TranslatedText: "世界",
		TargetLang:     targetLang,
	})

	payload := map[string]any{
		"q":      []string{"Hello", "World"},
		"source": "en",
		"target": "zh",
	}
	bodyBytes, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/translate", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp TranslateResponseBatch
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.TranslatedText) != 2 || resp.TranslatedText[0] != "你好" || resp.TranslatedText[1] != "世界" {
		t.Errorf("unexpected batch response: %#v", resp.TranslatedText)
	}
}

func TestFormUrlencodedFullLyrics(t *testing.T) {
	srv, store, cleanup := setupTestServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	fullSongLyrics := "Some deserts on this planet were oceans once\nSomewhere shrouded by the night, the sun will shine\nMay all the beauty be blessed"
	targetLang := "zh-Hans"

	// 预先写入整首歌词的翻译缓存
	cacheKey, normText := normalizer.GenerateCacheKey(fullSongLyrics, targetLang)
	translatedSong := "这颗星球上的某些沙漠曾经也是沧海\n黑夜笼罩的某处，太阳终将照耀\n愿所有的美好都能得到祝福"

	_ = store.Set(&storage.CachedTranslation{
		CacheKey:       cacheKey,
		SourceText:     normText,
		TranslatedText: translatedSong,
		SourceLang:     "en",
		TargetLang:     targetLang,
		ModelName:      "deepseek-flash",
	})

	// 模拟播放器发起的 application/x-www-form-urlencoded POST 请求
	form := "source=en&target=zh&q=" + strings.ReplaceAll(fullSongLyrics, "\n", "%0A")
	req := httptest.NewRequest("POST", "/translate", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp TranslateResponseSingle
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	expectedFirstLine := "这颗星球上的某些沙漠曾经也是沧海"
	if !strings.Contains(resp.TranslatedText, expectedFirstLine) {
		t.Errorf("expected translated text to contain %q, got: %s", expectedFirstLine, resp.TranslatedText)
	}
}

// TestInFlightDeduplication 验证并发请求相同未缓存歌词时，仅会触发一次底层大模型调用，避免重复 Token 消耗
func TestInFlightDeduplication(t *testing.T) {
	mgr := NewInFlightManager()
	var callCount int32

	fn := func(bgCtx context.Context) ([]string, error) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond) // 模拟大模型网络调用耗时
		return []string{"翻译行1", "翻译行2"}, nil
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	var res1, res2 []string
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		res1, err1 = mgr.ExecuteOrAttach(ctx, "dedup_test_key", 5*time.Second, fn)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond) // 稍后启动，确保进入同名任务附着逻辑
		res2, err2 = mgr.ExecuteOrAttach(ctx, "dedup_test_key", 5*time.Second, fn)
	}()
	wg.Wait()

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected error: err1=%v, err2=%v", err1, err2)
	}
	if len(res1) != 2 || len(res2) != 2 {
		t.Fatalf("unexpected results length: res1=%v, res2=%v", res1, res2)
	}
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected underlying fn to be called exactly 1 time, got %d", count)
	}
}

// TestInFlightClientCancellationContinues 验证客户端切歌中断 Context 后，后台任务仍会完整执行完毕
func TestInFlightClientCancellationContinues(t *testing.T) {
	mgr := NewInFlightManager()
	bgFinished := make(chan struct{})

	fn := func(bgCtx context.Context) ([]string, error) {
		time.Sleep(100 * time.Millisecond)
		close(bgFinished)
		return []string{"后台完成结果"}, nil
	}

	clientCtx, cancel := context.WithCancel(context.Background())
	// 20ms 后模拟切歌取消请求
	time.AfterFunc(20*time.Millisecond, cancel)

	res, err := mgr.ExecuteOrAttach(clientCtx, "cancel_test_key", 5*time.Second, fn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected caller to receive context.Canceled, got: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil res for cancelled caller, got: %v", res)
	}

	// 验证后台 Goroutine 依然完整跑完，未被客户端切歌影响
	select {
	case <-bgFinished:
		// 验证通过
	case <-time.After(500 * time.Millisecond):
		t.Fatal("background task failed to complete within timeout")
	}

	mgr.WaitAll(1 * time.Second)
}

// TestClientSongSwitchCancellationAndCacheHit 模拟切歌全链路测试：
// 1. 请求未缓存歌词，切歌取消请求；
// 2. 验证大模型请求在后台继续跑完并存入本地 SQLite；
// 3. 再次切回这首歌时，直接命中本地缓存，无需二次调用大模型！
func TestClientSongSwitchCancellationAndCacheHit(t *testing.T) {
	tmpDB := "test_song_switch.db"
	store, err := storage.InitDB(tmpDB)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer func() {
		store.Close()
		os.Remove(tmpDB)
		os.Remove(tmpDB + "-wal")
		os.Remove(tmpDB + "-shm")
	}()

	var llmCallCount int32

	// 启动 mock LLM API 服务
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&llmCallCount, 1)
		time.Sleep(60 * time.Millisecond) // 模拟大模型思考和吐字耗时

		resp := llm.ChatCompletionResponse{
			Choices: []struct {
				Message llm.ChatMessage `json:"message"`
			}{
				{
					Message: llm.ChatMessage{
						Role:    "assistant",
						Content: "星河长明\n长夜终尽",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockLLM.Close()

	cfg := &config.Config{
		ServerPort:        "5000",
		ServerHost:        "127.0.0.1",
		LLMPriority:       []string{"deepseek"},
		LLMProviders: map[string]config.ProviderConfig{
			"deepseek": {
				Type:           "openai",
				BaseURL:        mockLLM.URL,
				APIKey:         "test-key",
				Model:          "deepseek-flash",
				TimeoutSeconds: 5,
				MaxRetries:     1,
			},
		},
		PromptVersion:     "v1.0",
	}

	llmClient := llm.NewClient(cfg)
	srv := NewServer(cfg, store, llmClient)
	defer srv.Close()

	rawLrc := "[00:01.00]Stars shine forever\n[00:05.00]The long night will end"

	// 步骤 1：发起第一次请求，并模拟客户端在 20ms 后切歌（取消请求）
	clientCtx1, cancel1 := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel1)

	_, err1 := srv.translateSingleText(clientCtx1, rawLrc, "en", "zh-Hans", false)
	if !errors.Is(err1, context.Canceled) {
		t.Fatalf("expected context.Canceled for switched song, got: %v", err1)
	}

	// 等待后台任务落盘完成
	srv.inflight.WaitAll(2 * time.Second)

	// 步骤 2：验证数据已被成功落盘至 SQLite
	targetLang := srv.cfg.ResolveTargetLanguage("zh-Hans")
	cacheKey, _ := normalizer.GenerateCacheKey(rawLrc, targetLang)
	cached, err := store.Get(cacheKey)
	if err != nil || cached == nil {
		t.Fatalf("expected translation to be cached in SQLite after switch, got cached=%v, err=%v", cached, err)
	}
	if cached.TranslatedText != "星河长明\n长夜终尽" {
		t.Fatalf("unexpected cached content: %s", cached.TranslatedText)
	}

	// 步骤 3：模拟客户端切歌切回这首歌（正常发起请求）
	clientCtx2 := context.Background()
	res2, err2 := srv.translateSingleText(clientCtx2, rawLrc, "en", "zh-Hans", false)
	if err2 != nil {
		t.Fatalf("unexpected error on second request: %v", err2)
	}

	// 验证时间戳已无缝还原
	if !strings.Contains(res2, "[00:01.00] 星河长明") || !strings.Contains(res2, "[00:05.00] 长夜终尽") {
		t.Fatalf("expected rebuilt timestamp lyrics, got:\n%s", res2)
	}

	// 验证大模型 API 调用次数：全程只调用了一次，切回来直接命中缓存（0 MISS，0 重复调用）！
	if count := atomic.LoadInt32(&llmCallCount); count != 1 {
		t.Fatalf("expected LLM to be called only 1 time throughout both requests, got: %d", count)
	}
}

// TestWebUIAndManagementAPIs 验证单页面 Web 前端展示及歌词增删改查、提示词与优先级接口
func TestWebUIAndManagementAPIs(t *testing.T) {
	srv, store, cleanup := setupTestServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	// 1. 验证浏览器访问 / 返回 HTML 单页面
	reqUI := httptest.NewRequest("GET", "/", nil)
	reqUI.Header.Set("Accept", "text/html,application/xhtml+xml")
	wUI := httptest.NewRecorder()
	mux.ServeHTTP(wUI, reqUI)
	if wUI.Code != http.StatusOK || !strings.Contains(wUI.Body.String(), "AI-Lyrics-Translate") {
		t.Errorf("expected HTML Web UI, got status %d, body: %s", wUI.Code, wUI.Body.String()[:100])
	}

	// 2. 验证非 HTML 访问 / 返回系统 JSON 状态
	reqJSON := httptest.NewRequest("GET", "/", nil)
	reqJSON.Header.Set("Accept", "application/json")
	wJSON := httptest.NewRecorder()
	mux.ServeHTTP(wJSON, reqJSON)
	if wJSON.Code != http.StatusOK || !strings.Contains(wJSON.Body.String(), `"service":"AI-Lyrics-Translate"`) {
		t.Errorf("expected JSON status, got: %s", wJSON.Body.String())
	}

	// 3. 预先向存储中塞入一条歌词
	testKey := "testkey12345678"
	_ = store.Set(&storage.CachedTranslation{
		CacheKey:       testKey,
		SourceText:     "First line\nSecond line",
		TranslatedText: "第一行\n第二行",
		TargetLang:     "zh-Hans",
		ModelName:      "deepseek-flash",
	})

	// 4. GET /api/lyrics 查询列表
	reqList := httptest.NewRequest("GET", "/api/lyrics?limit=10", nil)
	wList := httptest.NewRecorder()
	mux.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("GET /api/lyrics failed: %d", wList.Code)
	}
	var listResp struct {
		Total int `json:"total"`
		Items []struct {
			CacheKey string `json:"cache_key"`
		} `json:"items"`
	}
	_ = json.Unmarshal(wList.Body.Bytes(), &listResp)
	if listResp.Total != 1 || len(listResp.Items) != 1 || listResp.Items[0].CacheKey != testKey {
		t.Fatalf("unexpected list response: %#v", listResp)
	}

	// 5. PUT /api/lyrics 修改歌词
	putBody, _ := json.Marshal(map[string]string{
		"key":             testKey,
		"translated_text": "第一行（已修改）\n第二行（已修改）",
	})
	reqPut := httptest.NewRequest("PUT", "/api/lyrics", bytes.NewBuffer(putBody))
	reqPut.Header.Set("Content-Type", "application/json")
	wPut := httptest.NewRecorder()
	mux.ServeHTTP(wPut, reqPut)
	if wPut.Code != http.StatusOK {
		t.Fatalf("PUT /api/lyrics failed: %d", wPut.Code)
	}

	// 校验修改生效
	updated, _ := store.Get(testKey)
	if updated.TranslatedText != "第一行（已修改）\n第二行（已修改）" {
		t.Errorf("lyrics text was not updated: %s", updated.TranslatedText)
	}

	// 6. GET /api/config 获取配置
	reqCfg := httptest.NewRequest("GET", "/api/config", nil)
	wCfg := httptest.NewRecorder()
	mux.ServeHTTP(wCfg, reqCfg)
	if wCfg.Code != http.StatusOK {
		t.Fatalf("GET /api/config failed: %d", wCfg.Code)
	}

	// 7. POST /api/config 更新用户提示词与优先级
	postCfgBody, _ := json.Marshal(map[string]any{
		"user_prompt": "更新后的诗意提示词",
		"priority":    []string{"deepseek"},
	})
	reqPostCfg := httptest.NewRequest("POST", "/api/config", bytes.NewBuffer(postCfgBody))
	reqPostCfg.Header.Set("Content-Type", "application/json")
	wPostCfg := httptest.NewRecorder()
	mux.ServeHTTP(wPostCfg, reqPostCfg)
	if wPostCfg.Code != http.StatusOK {
		t.Fatalf("POST /api/config failed: %d", wPostCfg.Code)
	}
	if srv.cfg.GetUserPrompt() != "更新后的诗意提示词" {
		t.Errorf("expected user prompt to be updated, got: %s", srv.cfg.GetUserPrompt())
	}

	// 8. DELETE /api/lyrics?key=... 删除单条
	reqDel := httptest.NewRequest("DELETE", "/api/lyrics?key="+testKey, nil)
	wDel := httptest.NewRecorder()
	mux.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("DELETE /api/lyrics failed: %d", wDel.Code)
	}
	count, _ := store.Count()
	if count != 0 {
		t.Errorf("expected 0 entries after delete, got %d", count)
	}
}

// TestRomanizeDetection 验证日韩语转罗马音请求参数识别
func TestRomanizeDetection(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Query: ?romaji=true
	r1 := httptest.NewRequest("POST", "/translate?romaji=true", nil)
	if !srv.checkRomanizeRequest(r1, nil) {
		t.Errorf("failed to detect romaji=true query")
	}

	// 2. Query: ?romanize=1
	r2 := httptest.NewRequest("POST", "/translate?romanize=1", nil)
	if !srv.checkRomanizeRequest(r2, nil) {
		t.Errorf("failed to detect romanize=1 query")
	}

	// 3. Target: romaji
	reqTarget := &TranslateRequest{Target: "romaji"}
	r3 := httptest.NewRequest("POST", "/translate", nil)
	if !srv.checkRomanizeRequest(r3, reqTarget) {
		t.Errorf("failed to detect target=romaji")
	}

	// 4. Header: X-Lyrics-Romanize: true
	r4 := httptest.NewRequest("POST", "/translate", nil)
	r4.Header.Set("X-Lyrics-Romanize", "true")
	if !srv.checkRomanizeRequest(r4, nil) {
		t.Errorf("failed to detect X-Lyrics-Romanize header")
	}

	// 5. Normal translate without romanize flag
	r5 := httptest.NewRequest("POST", "/translate", nil)
	reqNormal := &TranslateRequest{Target: "zh"}
	if srv.checkRomanizeRequest(r5, reqNormal) {
		t.Errorf("should not detect romanize for normal request")
	}
}


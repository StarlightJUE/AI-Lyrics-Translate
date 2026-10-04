package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
		LLMModel:       "deepseek-flash",
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

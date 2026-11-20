package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStorageCRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_lyrics.db")

	store, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = store.Close()
		}
	}()

	// 1. 测试初始为空
	count, err := store.Count()
	if err != nil || count != 0 {
		t.Fatalf("expected count 0, got %d, err=%v", count, err)
	}

	// 2. 插入记录
	entry1 := &CachedTranslation{
		CacheKey:       "key1",
		SourceText:     "When I was young",
		TranslatedText: "当我年少时",
		SourceLang:     "en",
		TargetLang:     "zh-Hans",
		ModelName:      "deepseek-flash",
		PromptVersion:  "v1.0",
	}
	if err := store.Set(entry1); err != nil {
		t.Fatalf("store.Set failed: %v", err)
	}

	entry2 := &CachedTranslation{
		CacheKey:       "key2",
		SourceText:     "Yesterday once more",
		TranslatedText: "昨日重现",
		SourceLang:     "en",
		TargetLang:     "zh-Hans",
		ModelName:      "gemini-2.0-flash",
		PromptVersion:  "v1.0",
	}
	if err := store.Set(entry2); err != nil {
		t.Fatalf("store.Set failed: %v", err)
	}

	// 3. 校验计数
	count, err = store.Count()
	if err != nil || count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	// 4. 单条查询
	got, err := store.Get("key1")
	if err != nil || got == nil || got.TranslatedText != "当我年少时" {
		t.Fatalf("store.Get failed: got=%v, err=%v", got, err)
	}

	// 5. 模糊搜索与列表
	list, total, err := store.List(10, 0, "昨日")
	if err != nil || total != 1 || len(list) != 1 || list[0].CacheKey != "key2" {
		t.Fatalf("store.List search failed: total=%d, len=%d, err=%v", total, len(list), err)
	}

	// 6. 手动修改歌词
	if err := store.UpdateTranslation("key1", "当我青春年少时"); err != nil {
		t.Fatalf("store.UpdateTranslation failed: %v", err)
	}
	updated, _ := store.Get("key1")
	if updated.TranslatedText != "当我青春年少时" {
		t.Fatalf("expected updated text, got %s", updated.TranslatedText)
	}

	// 7. 删除单条
	if err := store.Delete("key1"); err != nil {
		t.Fatalf("store.Delete failed: %v", err)
	}
	deleted, _ := store.Get("key1")
	if deleted != nil {
		t.Fatalf("expected deleted entry to be nil, got %v", deleted)
	}

	count, _ = store.Count()
	if count != 1 {
		t.Fatalf("expected count 1 after delete, got %d", count)
	}

	// 8. 清空全部
	if err := store.DeleteAll(); err != nil {
		t.Fatalf("store.DeleteAll failed: %v", err)
	}
	count, _ = store.Count()
	if count != 0 {
		t.Fatalf("expected count 0 after DeleteAll, got %d", count)
	}
}

func TestStoragePersistenceAndEdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "lyrics_cache.db")

	store, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = store.Close()
		}
	}()

	// 验证 UpdateTranslation 对不存在的 Key 返回错误
	if err := store.UpdateTranslation("nonexistent", "abc"); err == nil {
		t.Fatalf("expected error updating nonexistent key, got nil")
	}

	// 插入第一条记录并记录其 CreatedAt
	err = store.Set(&CachedTranslation{
		CacheKey:       "key_alpha",
		SourceText:     "Hello World",
		TranslatedText: "你好世界",
		SourceLang:     "en",
		TargetLang:     "zh",
		ModelName:      "model-v1",
		PromptVersion:  "v1.0",
	})
	if err != nil {
		t.Fatalf("Set alpha failed: %v", err)
	}
	alphaBefore, err := store.Get("key_alpha")
	if err != nil || alphaBefore == nil {
		t.Fatalf("Get alpha failed: %v", err)
	}

	// 等待 1.1s 以确保 SQLite 秒级 CURRENT_TIMESTAMP 能严格区分 UpdatedAt 顺序
	time.Sleep(1100 * time.Millisecond)

	// 插入第二条记录
	err = store.Set(&CachedTranslation{
		CacheKey:       "key_beta",
		SourceText:     "Moonlight Shadow",
		TranslatedText: "月光魅影",
		SourceLang:     "en",
		TargetLang:     "zh",
		ModelName:      "model-v1",
		PromptVersion:  "v1.0",
	})
	if err != nil {
		t.Fatalf("Set beta failed: %v", err)
	}

	// 验证 Upsert：保留 CreatedAt / SourceText / SourceLang / TargetLang，更新 TranslatedText / ModelName / PromptVersion
	err = store.Set(&CachedTranslation{
		CacheKey:       "key_alpha",
		SourceText:     "CHANGED SOURCE SHOULD NOT OVERWRITE",
		TranslatedText: "你好新世界",
		SourceLang:     "ja",
		TargetLang:     "ko",
		ModelName:      "model-v2",
		PromptVersion:  "v2.0",
	})
	if err != nil {
		t.Fatalf("Upsert alpha failed: %v", err)
	}
	alphaAfter, err := store.Get("key_alpha")
	if err != nil || alphaAfter == nil {
		t.Fatalf("Get alpha after upsert failed: %v", err)
	}
	if !alphaAfter.CreatedAt.Equal(alphaBefore.CreatedAt) {
		t.Errorf("expected CreatedAt preserved (%v), got %v", alphaBefore.CreatedAt, alphaAfter.CreatedAt)
	}
	if alphaAfter.SourceText != "Hello World" || alphaAfter.SourceLang != "en" || alphaAfter.TargetLang != "zh" {
		t.Errorf("expected source fields preserved on upsert, got %+v", alphaAfter)
	}
	if alphaAfter.TranslatedText != "你好新世界" || alphaAfter.ModelName != "model-v2" || alphaAfter.PromptVersion != "v2.0" {
		t.Errorf("expected translated fields updated on upsert, got %+v", alphaAfter)
	}

	// 验证大小写不敏感模糊搜索（SQLite LIKE 默认对 ASCII 大小写不敏感）
	searchList, searchTotal, err := store.List(10, 0, "moonlight")
	if err != nil || searchTotal != 1 || len(searchList) != 1 || searchList[0].CacheKey != "key_beta" {
		t.Fatalf("case-insensitive List search failed: total=%d, list=%+v, err=%v", searchTotal, searchList, err)
	}

	// 验证按 UpdatedAt 倒序排列与分页（alpha 被刚更新过，应排在 beta 前面）
	page1, total, err := store.List(1, 0, "")
	if err != nil || total != 2 || len(page1) != 1 || page1[0].CacheKey != "key_alpha" {
		t.Fatalf("List page1 expected key_alpha first (total=2), got total=%d, list=%+v, err=%v", total, page1, err)
	}
	page2, total2, err := store.List(1, 1, "")
	if err != nil || total2 != 2 || len(page2) != 1 || page2[0].CacheKey != "key_beta" {
		t.Fatalf("List page2 expected key_beta (total=2), got total=%d, list=%+v, err=%v", total2, page2, err)
	}
	pageOut, totalOut, err := store.List(1, 5, "")
	if err != nil || totalOut != 2 || len(pageOut) != 0 {
		t.Fatalf("List out-of-range offset expected 0 items (total=2), got total=%d, len=%d, err=%v", totalOut, len(pageOut), err)
	}

	// 并发读写验证
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				key := fmt.Sprintf("conc_%d_%d", gid, i)
				if err := store.Set(&CachedTranslation{
					CacheKey:       key,
					SourceText:     "Line " + key,
					TranslatedText: "行 " + key,
					SourceLang:     "en",
					TargetLang:     "zh",
					ModelName:      "conc-model",
					PromptVersion:  "v1.0",
				}); err != nil {
					errs <- err
					return
				}
				if got, err := store.Get(key); err != nil || got == nil {
					errs <- fmt.Errorf("concurrent Get %s failed: got=%v err=%v", key, got, err)
					return
				}
				if _, _, err := store.List(10, 0, "conc_"); err != nil {
					errs <- err
					return
				}
				if _, err := store.Count(); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent storage error: %v", err)
	}

	// 关闭并重新打开，验证落盘持久化完好
	savedPath := store.Path()
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	storeClosed = true
	if strings.Contains(store.BackendName(), "JSON") && !strings.HasSuffix(savedPath, ".json") {
		t.Fatalf("expected JSON backend path ending with .json, got %s", savedPath)
	}

	reopened, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("reopen InitDB failed: %v", err)
	}
	defer reopened.Close()

	cnt, err := reopened.Count()
	if err != nil || cnt != 18 {
		t.Fatalf("expected 18 persisted entries after reopen, got %d (err=%v)", cnt, err)
	}
	gotAlpha, err := reopened.Get("key_alpha")
	if err != nil || gotAlpha == nil || gotAlpha.TranslatedText != "你好新世界" {
		t.Fatalf("expected reopened key_alpha='你好新世界', got %+v (err=%v)", gotAlpha, err)
	}
}

func TestResolveJSONPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "lyrics_cache.json"},
		{"   ", "lyrics_cache.json"},
		{"lyrics_cache.db", "lyrics_cache.json"},
		{filepath.Join("tmp", "a.sqlite"), filepath.Join("tmp", "a.json")},
		{filepath.Join("tmp", "a.SQLITE3"), filepath.Join("tmp", "a.json")},
		{filepath.Join("tmp", "a.json"), filepath.Join("tmp", "a.json")},
		{filepath.Join("tmp", "cache"), filepath.Join("tmp", "cache.json")},
		{":memory:", ":memory:"},
	}
	for _, tc := range cases {
		got := resolveJSONPath(tc.in)
		if got != tc.want {
			t.Errorf("resolveJSONPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

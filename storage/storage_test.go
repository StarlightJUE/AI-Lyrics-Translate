package storage

import (
	"path/filepath"
	"testing"
)

func TestStorageCRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_lyrics.db")

	store, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer store.Close()

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

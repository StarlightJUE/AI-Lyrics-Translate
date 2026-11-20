package storage

import (
	"path/filepath"
	"strings"
	"time"
)

// CachedTranslation 对应缓存中的翻译实体
type CachedTranslation struct {
	CacheKey       string    `json:"cache_key"`
	SourceText     string    `json:"source_text"`
	TranslatedText string    `json:"translated_text"`
	SourceLang     string    `json:"source_lang"`
	TargetLang     string    `json:"target_lang"`
	ModelName      string    `json:"model_name"`
	PromptVersion  string    `json:"prompt_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// resolveJSONPath 将配置的数据库路径解析为纯 Go JSON 回退存储的文件路径
func resolveJSONPath(dbPath string) string {
	p := strings.TrimSpace(dbPath)
	if p == "" {
		return "lyrics_cache.json"
	}
	if p == ":memory:" {
		return ":memory:"
	}
	rawExt := filepath.Ext(p)
	ext := strings.ToLower(rawExt)
	switch ext {
	case ".json":
		return p
	case ".db", ".sqlite", ".sqlite3":
		return p[:len(p)-len(rawExt)] + ".json"
	default:
		return p + ".json"
	}
}

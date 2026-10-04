package storage

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// CachedTranslation 对应数据库中的缓存实体
type CachedTranslation struct {
	CacheKey       string
	SourceText     string
	TranslatedText string
	SourceLang     string
	TargetLang     string
	ModelName      string
	PromptVersion  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Storage 管理 SQLite 数据库连接及操作
type Storage struct {
	db *sql.DB
}

// InitDB 初始化数据库连接、开启 WAL 模式并执行初始化建表
func InitDB(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// 连接池优化
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	// 开启 WAL (Write-Ahead Logging) 模式与性能调优 PRAGMA
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			log.Printf("[WARN] Failed to set pragma %s: %v", pragma, err)
		}
	}

	// 创建歌词缓存表及索引
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS lyrics_cache (
		cache_key TEXT PRIMARY KEY,
		source_text TEXT NOT NULL,
		translated_text TEXT NOT NULL,
		source_lang TEXT,
		target_lang TEXT NOT NULL,
		model_name TEXT,
		prompt_version TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_lyrics_cache_target ON lyrics_cache (target_lang);
	`
	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize lyrics_cache table: %w", err)
	}

	return &Storage{db: db}, nil
}

// Get 查询指定 cacheKey 的缓存记录
func (s *Storage) Get(cacheKey string) (*CachedTranslation, error) {
	query := `
	SELECT cache_key, source_text, translated_text, source_lang, target_lang, model_name, prompt_version, created_at, updated_at
	FROM lyrics_cache
	WHERE cache_key = ?
	LIMIT 1;
	`
	var entry CachedTranslation
	err := s.db.QueryRow(query, cacheKey).Scan(
		&entry.CacheKey,
		&entry.SourceText,
		&entry.TranslatedText,
		&entry.SourceLang,
		&entry.TargetLang,
		&entry.ModelName,
		&entry.PromptVersion,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil // 缓存未命中
	}
	if err != nil {
		return nil, fmt.Errorf("query cache error: %w", err)
	}
	return &entry, nil
}

// Set 写入或更新缓存记录
func (s *Storage) Set(entry *CachedTranslation) error {
	upsertSQL := `
	INSERT INTO lyrics_cache (cache_key, source_text, translated_text, source_lang, target_lang, model_name, prompt_version, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	ON CONFLICT(cache_key) DO UPDATE SET
		translated_text = excluded.translated_text,
		model_name = excluded.model_name,
		prompt_version = excluded.prompt_version,
		updated_at = CURRENT_TIMESTAMP;
	`
	_, err := s.db.Exec(upsertSQL,
		entry.CacheKey,
		entry.SourceText,
		entry.TranslatedText,
		entry.SourceLang,
		entry.TargetLang,
		entry.ModelName,
		entry.PromptVersion,
	)
	if err != nil {
		return fmt.Errorf("insert/update cache error: %w", err)
	}
	return nil
}

// Close 关闭数据库连接
func (s *Storage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

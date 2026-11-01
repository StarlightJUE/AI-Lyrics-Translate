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

// List 分页查询缓存列表，支持按原歌词、翻译歌词或 Key 进行模糊搜索
func (s *Storage) List(limit, offset int, keyword string) ([]*CachedTranslation, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var countQuery string
	var listQuery string
	var args []any
	var countArgs []any

	if keyword != "" {
		pattern := "%" + keyword + "%"
		countQuery = "SELECT COUNT(*) FROM lyrics_cache WHERE source_text LIKE ? OR translated_text LIKE ? OR cache_key LIKE ?"
		countArgs = append(countArgs, pattern, pattern, pattern)
		listQuery = `
		SELECT cache_key, source_text, translated_text, source_lang, target_lang, model_name, prompt_version, created_at, updated_at
		FROM lyrics_cache
		WHERE source_text LIKE ? OR translated_text LIKE ? OR cache_key LIKE ?
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?;
		`
		args = append(args, pattern, pattern, pattern, limit, offset)
	} else {
		countQuery = "SELECT COUNT(*) FROM lyrics_cache"
		listQuery = `
		SELECT cache_key, source_text, translated_text, source_lang, target_lang, model_name, prompt_version, created_at, updated_at
		FROM lyrics_cache
		ORDER BY updated_at DESC
		LIMIT ? OFFSET ?;
		`
		args = append(args, limit, offset)
	}

	var total int
	if err := s.db.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count lyrics error: %w", err)
	}

	rows, err := s.db.Query(listQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list lyrics error: %w", err)
	}
	defer rows.Close()

	var list []*CachedTranslation
	for rows.Next() {
		var entry CachedTranslation
		if err := rows.Scan(
			&entry.CacheKey,
			&entry.SourceText,
			&entry.TranslatedText,
			&entry.SourceLang,
			&entry.TargetLang,
			&entry.ModelName,
			&entry.PromptVersion,
			&entry.CreatedAt,
			&entry.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan lyrics row error: %w", err)
		}
		list = append(list, &entry)
	}

	return list, total, nil
}

// UpdateTranslation 手动修改更新某条歌词的翻译内容
func (s *Storage) UpdateTranslation(cacheKey, translatedText string) error {
	updateSQL := `
	UPDATE lyrics_cache
	SET translated_text = ?, updated_at = CURRENT_TIMESTAMP
	WHERE cache_key = ?;
	`
	res, err := s.db.Exec(updateSQL, translatedText, cacheKey)
	if err != nil {
		return fmt.Errorf("update translation error: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("cache key not found: %s", cacheKey)
	}
	return nil
}

// Delete 删除指定 cacheKey 的缓存记录
func (s *Storage) Delete(cacheKey string) error {
	deleteSQL := "DELETE FROM lyrics_cache WHERE cache_key = ?;"
	_, err := s.db.Exec(deleteSQL, cacheKey)
	if err != nil {
		return fmt.Errorf("delete cache error: %w", err)
	}
	return nil
}

// DeleteAll 清空所有歌词缓存记录
func (s *Storage) DeleteAll() error {
	deleteSQL := "DELETE FROM lyrics_cache;"
	_, err := s.db.Exec(deleteSQL)
	if err != nil {
		return fmt.Errorf("delete all cache error: %w", err)
	}
	return nil
}

// Count 获取当前缓存总条数
func (s *Storage) Count() (int, error) {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM lyrics_cache;").Scan(&count); err != nil {
		return 0, fmt.Errorf("count cache error: %w", err)
	}
	return count, nil
}

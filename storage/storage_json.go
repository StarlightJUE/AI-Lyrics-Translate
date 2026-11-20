//go:build jsonstorage || !((darwin && (amd64 || arm64)) || (windows && (386 || amd64 || arm64)) || (linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64 || s390x)) || (android && arm64) || (freebsd && (386 || amd64 || arm || arm64)) || (netbsd && amd64) || (openbsd && (amd64 || arm64)))

package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type jsonCacheFile struct {
	Version int                           `json:"version"`
	Entries map[string]*CachedTranslation `json:"entries"`
}

// Storage 纯 Go JSON 回退存储实现（用于无 modernc.org/sqlite 支持的冷门/实验架构）
type Storage struct {
	mu         sync.RWMutex
	path       string
	memoryOnly bool
	entries    map[string]*CachedTranslation
}

// InitDB 初始化 JSON 文件缓存，若已存在则加载至内存，否则初始化空缓存文件
func InitDB(dbPath string) (*Storage, error) {
	resolved := resolveJSONPath(dbPath)
	s := &Storage{
		path:       resolved,
		memoryOnly: resolved == ":memory:",
		entries:    make(map[string]*CachedTranslation),
	}

	if s.memoryOnly {
		return s, nil
	}

	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create cache directory: %w", err)
		}
	}

	data, err := os.ReadFile(s.path)
	if err == nil {
		if strings.TrimSpace(string(data)) != "" {
			var fileData jsonCacheFile
			if err := json.Unmarshal(data, &fileData); err != nil {
				return nil, fmt.Errorf("failed to parse json cache file %s: %w", s.path, err)
			}
			if fileData.Entries != nil {
				s.entries = fileData.Entries
			}
		}
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to read json cache file %s: %w", s.path, err)
	}

	// 文件不存在时先落盘空结构并校验目录可写性
	if err := s.persistLocked(); err != nil {
		return nil, fmt.Errorf("failed to initialize json cache file %s: %w", s.path, err)
	}

	return s, nil
}

// Path 返回当前缓存 JSON 文件路径
func (s *Storage) Path() string {
	return s.path
}

// BackendName 返回当前存储后端描述名称
func (s *Storage) BackendName() string {
	return "Pure-Go JSON Fallback"
}

func cloneEntry(e *CachedTranslation) *CachedTranslation {
	if e == nil {
		return nil
	}
	cp := *e
	return &cp
}

// persistLocked 在持有写锁的前提下将内存索引原子序列化落盘
func (s *Storage) persistLocked() error {
	if s.memoryOnly {
		return nil
	}

	payload := jsonCacheFile{
		Version: 1,
		Entries: s.entries,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json cache error: %w", err)
	}

	dir := filepath.Dir(s.path)
	if dir == "" {
		dir = "."
	}

	tmpFile, err := os.CreateTemp(dir, ".lyrics_cache-*.tmp")
	if err != nil {
		// 兼容无传统临时文件行为的特殊文件系统
		if writeErr := os.WriteFile(s.path, data, 0o644); writeErr != nil {
			return fmt.Errorf("create temp file error (%v) and direct write error (%w)", err, writeErr)
		}
		return nil
	}
	tmpPath := tmpFile.Name()

	cleanupTmp := func() {
		_ = os.Remove(tmpPath)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		cleanupTmp()
		return fmt.Errorf("write temp cache error: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		cleanupTmp()
		return fmt.Errorf("sync temp cache error: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanupTmp()
		return fmt.Errorf("close temp cache error: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		// Plan 9 / 部分 Windows 场景下目标存在时 Rename 可能失败，尝试移除后重命名，失败则直接覆盖写入
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmpPath, s.path); retryErr != nil {
			cleanupTmp()
			if writeErr := os.WriteFile(s.path, data, 0o644); writeErr != nil {
				return fmt.Errorf("rename cache error (%v) and fallback write error (%w)", retryErr, writeErr)
			}
		}
	}

	return nil
}

// Get 查询指定 cacheKey 的缓存记录
func (s *Storage) Get(cacheKey string) (*CachedTranslation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.entries[cacheKey]
	if !ok {
		return nil, nil
	}
	return cloneEntry(entry), nil
}

// Set 写入或更新缓存记录（保持与 SQLite Upsert 一致的字段保留语义）
func (s *Storage) Set(entry *CachedTranslation) error {
	if entry == nil {
		return fmt.Errorf("cache entry is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var prev *CachedTranslation
	hasPrev := false

	if existing, ok := s.entries[entry.CacheKey]; ok {
		hasPrev = true
		prev = cloneEntry(existing)
		updated := cloneEntry(existing)
		updated.TranslatedText = entry.TranslatedText
		updated.ModelName = entry.ModelName
		updated.PromptVersion = entry.PromptVersion
		updated.UpdatedAt = now
		s.entries[entry.CacheKey] = updated
	} else {
		stored := cloneEntry(entry)
		stored.CreatedAt = now
		stored.UpdatedAt = now
		s.entries[entry.CacheKey] = stored
	}

	if err := s.persistLocked(); err != nil {
		if hasPrev {
			s.entries[entry.CacheKey] = prev
		} else {
			delete(s.entries, entry.CacheKey)
		}
		return fmt.Errorf("insert/update cache error: %w", err)
	}
	return nil
}

// Close 关闭存储实例
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}

// List 分页查询缓存列表，支持按原歌词、翻译歌词或 Key 进行大小写不敏感模糊搜索
func (s *Storage) List(limit, offset int, keyword string) ([]*CachedTranslation, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	kwLower := strings.ToLower(keyword)
	filtered := make([]*CachedTranslation, 0, len(s.entries))
	for _, entry := range s.entries {
		if kwLower != "" {
			if !strings.Contains(strings.ToLower(entry.SourceText), kwLower) &&
				!strings.Contains(strings.ToLower(entry.TranslatedText), kwLower) &&
				!strings.Contains(strings.ToLower(entry.CacheKey), kwLower) {
				continue
			}
		}
		filtered = append(filtered, cloneEntry(entry))
	}

	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].CacheKey < filtered[j].CacheKey
	})

	total := len(filtered)
	if offset >= total {
		return nil, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

// UpdateTranslation 手动修改更新某条歌词的翻译内容
func (s *Storage) UpdateTranslation(cacheKey, translatedText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.entries[cacheKey]
	if !ok {
		return fmt.Errorf("cache key not found: %s", cacheKey)
	}

	prev := cloneEntry(existing)
	existing.TranslatedText = translatedText
	existing.UpdatedAt = time.Now().UTC()

	if err := s.persistLocked(); err != nil {
		s.entries[cacheKey] = prev
		return fmt.Errorf("update translation error: %w", err)
	}
	return nil
}

// Delete 删除指定 cacheKey 的缓存记录
func (s *Storage) Delete(cacheKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.entries[cacheKey]
	if !ok {
		return nil
	}
	prev := cloneEntry(existing)
	delete(s.entries, cacheKey)

	if err := s.persistLocked(); err != nil {
		s.entries[cacheKey] = prev
		return fmt.Errorf("delete cache error: %w", err)
	}
	return nil
}

// DeleteAll 清空所有歌词缓存记录
func (s *Storage) DeleteAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.entries
	s.entries = make(map[string]*CachedTranslation)

	if err := s.persistLocked(); err != nil {
		s.entries = prev
		return fmt.Errorf("delete all cache error: %w", err)
	}
	return nil
}

// Count 获取当前缓存总条数
func (s *Storage) Count() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries), nil
}

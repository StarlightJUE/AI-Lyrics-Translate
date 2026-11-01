package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// DefaultConfigYAML 当完全找不到配置文件时自动生成的标准模版内容（支持双路大模型与优先级）
const DefaultConfigYAML = `# ==============================================================
# AI-Lyrics-Translate 配置文件 (config.yaml)
# ==============================================================

# 服务基础设置
server:
  host: "0.0.0.0"       # 监听地址
  port: "5000"          # 业务监听端口 (兼容 LibreTranslate 默认 5000)
  admin_port: ""        # 管理后台独立监听端口 (可选，如 "5001"；留空则与业务端口共用)

# 双路大模型 API 设置 (支持 DeepSeek 与 Gemini，支持手动指定优先级与故障自动切换)
llm:
  # 优先调用顺序列表：首个不可用时自动顺延切换下一个
  # 可选: ["deepseek", "gemini"] 或 ["gemini", "deepseek"]
  priority:
    - "deepseek"
    - "gemini"

  # 提供商详细配置
  providers:
    # 1. DeepSeek 官方或各类第三方 OpenAI 兼容端点 (SiliconFlow / 中转站 / Ollama 等)
    deepseek:
      type: "openai"                       # 协议类型: openai
      base_url: "https://api.deepseek.com"  # 官方或第三方 API 端点
      api_key: ""                           # 你的 API 密钥 (例如 sk-...)
      model: "deepseek-chat"                # 模型名称 (如 deepseek-chat / deepseek-flash)
      temperature: 0.2                      # 采样温度 (0.0 ~ 1.0)
      timeout_seconds: 60                   # 单次请求超时时间（秒）
      max_retries: 2                        # 失败最大重试次数

    # 2. Google Gemini 官方原生端点 / 第三方兼容端点
    gemini:
      type: "gemini"                       # 协议类型: gemini (官方 REST) 或 openai (第三方兼容)
      base_url: "https://generativelanguage.googleapis.com/v1beta"
      api_key: ""                           # Google Gemini API 密钥 (AIza...)
      model: "gemini-3.5-flash"             # 模型名称 (如 gemini-3.5-flash)
      temperature: 0.2
      timeout_seconds: 60
      max_retries: 2

# 翻译与提示词设置
translation:
  # 当客户端仅传入泛指的 "zh" 时，默认输出简体还是繁体：
  # 可选值: simplified (简体中文) / traditional (繁体中文)
  chinese_target_default: "simplified"
  
  # 提示词版本标识（当提示词重大更新时，修改此项可区分缓存）
  prompt_version: "v1.0"
  
  # 用户自定义提示词（可选，留空则默认使用系统内置的高品质文学级歌词翻译Prompt）
  custom_prompt: ""

  # 自定义元提示词（可选，留空则使用内置严格行数对应、纯净格式与防背景信息泄漏框架）
  custom_meta_prompt: ""

# 本地 SQLite 缓存数据库设置
storage:
  db_path: "lyrics_cache.db"

# 日志级别 (debug / info / warn / error)
log_level: "info"
`

// ProviderConfig 单个大模型提供商配置
type ProviderConfig struct {
	Type           string  `yaml:"type" json:"type"`                       // "openai" 或 "gemini"
	BaseURL        string  `yaml:"base_url" json:"base_url"`               // API 根地址
	APIKey         string  `yaml:"api_key" json:"api_key"`                 // API 密钥
	Model          string  `yaml:"model" json:"model"`                     // 调用的模型名称
	Temperature    float64 `yaml:"temperature" json:"temperature"`         // 采样温度
	TimeoutSeconds int     `yaml:"timeout_seconds" json:"timeout_seconds"` // 超时时间（秒）
	MaxRetries     int     `yaml:"max_retries" json:"max_retries"`         // 失败重试次数
}

// FileConfig 对应 YAML / JSON 配置文件模型
type FileConfig struct {
	Server struct {
		Host      string `yaml:"host" json:"host"`
		Port      string `yaml:"port" json:"port"`
		AdminPort string `yaml:"admin_port" json:"admin_port"`
	} `yaml:"server" json:"server"`
	LLM struct {
		Priority       []string                  `yaml:"priority" json:"priority"`
		Providers      map[string]ProviderConfig `yaml:"providers" json:"providers"`
		// 兼容旧版扁平字段
		BaseURL        string                    `yaml:"base_url" json:"base_url"`
		APIKey         string                    `yaml:"api_key" json:"api_key"`
		Model          string                    `yaml:"model" json:"model"`
		Temperature    float64                   `yaml:"temperature" json:"temperature"`
		TimeoutSeconds int                       `yaml:"timeout_seconds" json:"timeout_seconds"`
		MaxRetries     int                       `yaml:"max_retries" json:"max_retries"`
	} `yaml:"llm" json:"llm"`
	Translation struct {
		ChineseTargetDefault string `yaml:"chinese_target_default" json:"chinese_target_default"`
		PromptVersion        string `yaml:"prompt_version" json:"prompt_version"`
		CustomPrompt         string `yaml:"custom_prompt" json:"custom_prompt"`
		CustomMetaPrompt     string `yaml:"custom_meta_prompt" json:"custom_meta_prompt"`
	} `yaml:"translation" json:"translation"`
	Lyrics struct {
		ChineseTargetDefault string `yaml:"chinese_target_default" json:"chinese_target_default"`
		PromptVersion        string `yaml:"prompt_version" json:"prompt_version"`
		CustomPrompt         string `yaml:"custom_prompt" json:"custom_prompt"`
		CustomMetaPrompt     string `yaml:"custom_meta_prompt" json:"custom_meta_prompt"`
	} `yaml:"lyrics" json:"lyrics"`
	Storage struct {
		DBPath string `yaml:"db_path" json:"db_path"`
	} `yaml:"storage" json:"storage"`
	LogLevel string `yaml:"log_level" json:"log_level"`
}

// Config 存储服务运行时全局配置
type Config struct {
	mu                   sync.RWMutex
	ConfigSource         string // 记录配置来源（如 config.yaml / .env 等）
	ServerPort           string
	ServerHost           string
	AdminPort            string // 独立管理端端口 (留空则共用 ServerPort)

	// 双路模型配置
	LLMPriority          []string                  // ["deepseek", "gemini"]
	LLMProviders         map[string]ProviderConfig // deepseek -> ..., gemini -> ...

	// 兼容快捷字段（同步当前最高优先级的模型信息）
	LLMBaseURL           string
	LLMAPIKey            string
	LLMModel             string
	LLMTemperature       float64
	LLMTimeoutSeconds    int
	LLMMaxRetries        int

	PromptVersion        string
	CustomPrompt         string // 用户提示词 (User Prompt)
	CustomMetaPrompt     string // 自定义元提示词 (Meta Prompt)
	ChineseTargetDefault string
	DBPath               string
	LogLevel             string
}

// LoadConfig 加载系统配置。
// 优先级：指定配置文件路径 -> 当前及可执行程序目录下的 config.yaml/json -> .env 文件 -> 环境变量 -> 自动生成默认 config.yaml
func LoadConfig(specifiedPath ...string) *Config {
	var targetPath string
	if len(specifiedPath) > 0 && specifiedPath[0] != "" {
		targetPath = specifiedPath[0]
	}

	cfg := &Config{
		ServerPort:           "5000",
		ServerHost:           "0.0.0.0",
		LLMPriority:          []string{"deepseek", "gemini"},
		LLMProviders:         defaultProviders(),
		PromptVersion:        "v1.0",
		CustomPrompt:         "",
		ChineseTargetDefault: "simplified",
		DBPath:               "lyrics_cache.db",
		LogLevel:             "info",
		ConfigSource:         "系统环境变量 / 默认值",
	}

	// 1. 查找配置文件
	targetConfigPath := findConfigFile(targetPath)

	if targetConfigPath != "" {
		if err := loadFromFile(targetConfigPath, cfg); err == nil {
			cfg.ConfigSource = targetConfigPath
		} else {
			log.Printf("[WARN] 读取配置文件 %s 失败: %v，尝试降级加载", targetConfigPath, err)
		}
	} else {
		// 未找到配置文件时，尝试加载 .env 垫底
		for _, envPath := range []string{".env", "../.env", "../../.env"} {
			if _, err := os.Stat(envPath); err == nil {
				_ = godotenv.Load(envPath)
				cfg.ConfigSource = envPath
				break
			}
		}

		// 若既没有 config.yaml 也没有 .env，自动在当前目录创建 config.yaml 方便用户直接修改
		if targetPath == "" && cfg.ConfigSource == "系统环境变量 / 默认值" {
			const autoPath = "config.yaml"
			if err := os.WriteFile(autoPath, []byte(DefaultConfigYAML), 0644); err == nil {
				cfg.ConfigSource = autoPath + " (首次运行自动创建)"
				_ = loadFromFile(autoPath, cfg)
				fmt.Println("[提示] 检测到首次运行，已自动生成默认配置文件 config.yaml，请填入你的 API Key 后启动！")
			}
		}
	}

	// 2. 若未在配置文件中配置的字段，允许系统环境变量进行补充/覆盖（容器化环境支持）
	overrideWithEnv(cfg)

	// 3. 刷新快捷属性
	cfg.syncActiveProviderFields()

	return cfg
}

func defaultProviders() map[string]ProviderConfig {
	return map[string]ProviderConfig{
		"deepseek": {
			Type:           "openai",
			BaseURL:        "https://api.deepseek.com",
			APIKey:         "",
			Model:          "deepseek-flash",
			Temperature:    0.2,
			TimeoutSeconds: 60,
			MaxRetries:     2,
		},
		"gemini": {
			Type:           "gemini",
			BaseURL:        "https://generativelanguage.googleapis.com/v1beta",
			APIKey:         "",
			Model:          "gemini-3.5-flash",
			Temperature:    0.2,
			TimeoutSeconds: 60,
			MaxRetries:     2,
		},
	}
}

func findConfigFile(specified string) string {
	if specified != "" {
		if _, err := os.Stat(specified); err == nil {
			return specified
		}
		log.Printf("[WARN] 指定的配置文件路径不存在: %s", specified)
	}

	var candidates []string
	candidates = append(candidates, "config.yaml", "config.yml", "config.json")

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "config.yaml"),
			filepath.Join(exeDir, "config.yml"),
			filepath.Join(exeDir, "config.json"),
		)
	}

	candidates = append(candidates,
		"../config.yaml", "../config.yml", "../config.json",
		"../../config.yaml", "../../config.yml", "../../config.json",
	)

	for _, path := range candidates {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path
		}
	}
	return ""
}

func loadFromFile(filePath string, cfg *Config) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var f FileConfig
	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".json" {
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("解析 json 失败: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("解析 yaml 失败: %w", err)
		}
	}

	// 映射到全局 Config
	if f.Server.Port != "" {
		cfg.ServerPort = f.Server.Port
	}
	if f.Server.Host != "" {
		cfg.ServerHost = f.Server.Host
	}
	if f.Server.AdminPort != "" {
		cfg.AdminPort = f.Server.AdminPort
	}

	// 优先级解析
	if len(f.LLM.Priority) > 0 {
		var validPriority []string
		for _, p := range f.LLM.Priority {
			p = strings.ToLower(strings.TrimSpace(p))
			if p != "" {
				validPriority = append(validPriority, p)
			}
		}
		if len(validPriority) > 0 {
			cfg.LLMPriority = validPriority
		}
	}

	// 初始化 Providers 字典
	if cfg.LLMProviders == nil {
		cfg.LLMProviders = defaultProviders()
	}

	// 1. 如果配置了 providers 节点
	if f.LLM.Providers != nil {
		for name, p := range f.LLM.Providers {
			key := strings.ToLower(strings.TrimSpace(name))
			existing := cfg.LLMProviders[key]
			if p.Type != "" {
				existing.Type = p.Type
			}
			if p.BaseURL != "" {
				existing.BaseURL = p.BaseURL
			}
			if p.APIKey != "" {
				existing.APIKey = p.APIKey
			}
			if p.Model != "" {
				existing.Model = p.Model
			}
			if p.Temperature > 0 {
				existing.Temperature = p.Temperature
			}
			if p.TimeoutSeconds > 0 {
				existing.TimeoutSeconds = p.TimeoutSeconds
			}
			if p.MaxRetries > 0 {
				existing.MaxRetries = p.MaxRetries
			}
			cfg.LLMProviders[key] = existing
		}
	}

	// 2. 兼容旧版 llm.* 扁平配置（若设置了，自动赋给 deepseek）
	if f.LLM.BaseURL != "" || f.LLM.APIKey != "" || f.LLM.Model != "" {
		ds := cfg.LLMProviders["deepseek"]
		if f.LLM.BaseURL != "" {
			ds.BaseURL = f.LLM.BaseURL
		}
		if f.LLM.APIKey != "" {
			ds.APIKey = f.LLM.APIKey
		}
		if f.LLM.Model != "" {
			ds.Model = f.LLM.Model
		}
		if f.LLM.Temperature > 0 {
			ds.Temperature = f.LLM.Temperature
		}
		if f.LLM.TimeoutSeconds > 0 {
			ds.TimeoutSeconds = f.LLM.TimeoutSeconds
		}
		if f.LLM.MaxRetries > 0 {
			ds.MaxRetries = f.LLM.MaxRetries
		}
		cfg.LLMProviders["deepseek"] = ds
	}

	// 兼容 translation 或 lyrics 配置块
	targetZh := f.Translation.ChineseTargetDefault
	if targetZh == "" {
		targetZh = f.Lyrics.ChineseTargetDefault
	}
	if targetZh != "" {
		cfg.ChineseTargetDefault = targetZh
	}

	pVer := f.Translation.PromptVersion
	if pVer == "" {
		pVer = f.Lyrics.PromptVersion
	}
	if pVer != "" {
		cfg.PromptVersion = pVer
	}

	cPrompt := f.Translation.CustomPrompt
	if cPrompt == "" {
		cPrompt = f.Lyrics.CustomPrompt
	}
	if cPrompt != "" {
		cfg.CustomPrompt = cPrompt
	}

	cMeta := f.Translation.CustomMetaPrompt
	if cMeta == "" {
		cMeta = f.Lyrics.CustomMetaPrompt
	}
	if cMeta != "" {
		cfg.CustomMetaPrompt = cMeta
	}

	if f.Storage.DBPath != "" {
		cfg.DBPath = f.Storage.DBPath
	}
	if f.LogLevel != "" {
		cfg.LogLevel = f.LogLevel
	}

	return nil
}

func overrideWithEnv(cfg *Config) {
	if val := os.Getenv("SERVER_PORT"); val != "" {
		cfg.ServerPort = val
	}
	if val := os.Getenv("SERVER_HOST"); val != "" {
		cfg.ServerHost = val
	}
	if val := os.Getenv("ADMIN_PORT"); val != "" {
		cfg.AdminPort = val
	} else if val := os.Getenv("SERVER_ADMIN_PORT"); val != "" {
		cfg.AdminPort = val
	}

	// 优先级环境变量 LLM_PRIORITY=deepseek,gemini
	if val := os.Getenv("LLM_PRIORITY"); val != "" {
		parts := strings.Split(val, ",")
		var pri []string
		for _, p := range parts {
			p = strings.ToLower(strings.TrimSpace(p))
			if p != "" {
				pri = append(pri, p)
			}
		}
		if len(pri) > 0 {
			cfg.LLMPriority = pri
		}
	}

	// DeepSeek 环境变量
	ds := cfg.LLMProviders["deepseek"]
	if val := os.Getenv("DEEPSEEK_BASE_URL"); val != "" {
		ds.BaseURL = val
	} else if ds.BaseURL == "" || ds.BaseURL == "https://api.deepseek.com" {
		if val := os.Getenv("LLM_BASE_URL"); val != "" {
			ds.BaseURL = val
		}
	}
	if val := os.Getenv("DEEPSEEK_API_KEY"); val != "" {
		ds.APIKey = val
	} else if ds.APIKey == "" {
		if val := os.Getenv("LLM_API_KEY"); val != "" {
			ds.APIKey = val
		}
	}
	if val := os.Getenv("DEEPSEEK_MODEL"); val != "" {
		ds.Model = val
	} else if ds.Model == "" || ds.Model == "deepseek-flash" {
		if val := os.Getenv("LLM_MODEL"); val != "" {
			ds.Model = val
		}
	}
	if val := os.Getenv("DEEPSEEK_TEMPERATURE"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			ds.Temperature = f
		}
	}
	cfg.LLMProviders["deepseek"] = ds

	// Gemini 环境变量
	gem := cfg.LLMProviders["gemini"]
	if val := os.Getenv("GEMINI_BASE_URL"); val != "" {
		gem.BaseURL = val
	}
	if val := os.Getenv("GEMINI_API_KEY"); val != "" {
		gem.APIKey = val
	}
	if val := os.Getenv("GEMINI_MODEL"); val != "" {
		gem.Model = val
	}
	if val := os.Getenv("GEMINI_TEMPERATURE"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			gem.Temperature = f
		}
	}
	cfg.LLMProviders["gemini"] = gem

	if val := os.Getenv("PROMPT_VERSION"); val != "" {
		cfg.PromptVersion = val
	}
	if val := os.Getenv("CUSTOM_PROMPT"); val != "" {
		cfg.CustomPrompt = val
	}
	if val := os.Getenv("CUSTOM_META_PROMPT"); val != "" {
		cfg.CustomMetaPrompt = val
	}
	if val := os.Getenv("CHINESE_TARGET_DEFAULT"); val != "" {
		cfg.ChineseTargetDefault = val
	}
	if val := os.Getenv("DB_PATH"); val != "" {
		cfg.DBPath = val
	}
	if val := os.Getenv("LOG_LEVEL"); val != "" {
		cfg.LogLevel = val
	}
}

// syncActiveProviderFields 同步最高优先级有效模型的快捷字段
func (c *Config) syncActiveProviderFields() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, name := range c.LLMPriority {
		if p, ok := c.LLMProviders[name]; ok {
			c.LLMBaseURL = p.BaseURL
			c.LLMAPIKey = p.APIKey
			c.LLMModel = p.Model
			c.LLMTemperature = p.Temperature
			c.LLMTimeoutSeconds = p.TimeoutSeconds
			c.LLMMaxRetries = p.MaxRetries
			return
		}
	}
}

// GetPriority 线程安全获取优先级列表
func (c *Config) GetPriority() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make([]string, len(c.LLMPriority))
	copy(res, c.LLMPriority)
	return res
}

// GetProviders 线程安全获取提供商列表
func (c *Config) GetProviders() map[string]ProviderConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make(map[string]ProviderConfig, len(c.LLMProviders))
	for k, v := range c.LLMProviders {
		res[k] = v
	}
	return res
}

// GetAdminPort 获取独立管理端端口
func (c *Config) GetAdminPort() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AdminPort
}

// GetUserPrompt 获取用户自定义提示词
func (c *Config) GetUserPrompt() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.CustomPrompt
}

// GetMetaPrompt 获取元提示词（若未自定义则返回空，调用处将回退为系统默认元提示词）
func (c *Config) GetMetaPrompt() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.CustomMetaPrompt
}

// UpdateUserPromptAndSave 更新用户提示词并写回配置文件
func (c *Config) UpdateUserPromptAndSave(newPrompt string) error {
	c.mu.Lock()
	c.CustomPrompt = newPrompt
	c.mu.Unlock()

	return c.SaveToFile()
}

// UpdateMetaPromptAndSave 更新元提示词并写回配置文件
func (c *Config) UpdateMetaPromptAndSave(newMeta string) error {
	c.mu.Lock()
	c.CustomMetaPrompt = newMeta
	c.mu.Unlock()

	return c.SaveToFile()
}

// UpdateAdminPortAndSave 更新管理端口并写回配置文件
func (c *Config) UpdateAdminPortAndSave(newPort string) error {
	c.mu.Lock()
	c.AdminPort = strings.TrimSpace(newPort)
	c.mu.Unlock()

	return c.SaveToFile()
}

// UpdatePriorityAndSave 更新优先级并写回配置文件
func (c *Config) UpdatePriorityAndSave(priority []string) error {
	c.mu.Lock()
	c.LLMPriority = priority
	c.mu.Unlock()

	c.syncActiveProviderFields()
	return c.SaveToFile()
}

// UpdateProviderAndSave 更新某个提供商并写回配置文件
func (c *Config) UpdateProviderAndSave(name string, p ProviderConfig) error {
	c.mu.Lock()
	c.LLMProviders[name] = p
	c.mu.Unlock()

	c.syncActiveProviderFields()
	return c.SaveToFile()
}

// SaveToFile 将当前配置持久化保存至配置文件
func (c *Config) SaveToFile(customPath ...string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	targetPath := c.ConfigSource
	if len(customPath) > 0 && customPath[0] != "" {
		targetPath = customPath[0]
	}
	if strings.Contains(targetPath, "(") || strings.Contains(targetPath, "默认值") || targetPath == "" {
		targetPath = "config.yaml"
	}

	var f FileConfig
	f.Server.Host = c.ServerHost
	f.Server.Port = c.ServerPort
	f.Server.AdminPort = c.AdminPort
	f.LLM.Priority = c.LLMPriority
	f.LLM.Providers = c.LLMProviders
	f.Translation.ChineseTargetDefault = c.ChineseTargetDefault
	f.Translation.PromptVersion = c.PromptVersion
	f.Translation.CustomPrompt = c.CustomPrompt
	f.Translation.CustomMetaPrompt = c.CustomMetaPrompt
	f.Storage.DBPath = c.DBPath
	f.LogLevel = c.LogLevel

	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal config to yaml failed: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("write config file error: %w", err)
	}
	return nil
}

// ResolveTargetLanguage 根据客户端传入的 target 与配置，精准解析简繁体及通用语言代码
func (c *Config) ResolveTargetLanguage(rawTarget string) string {
	t := strings.ToLower(strings.TrimSpace(rawTarget))

	// 1. 若客户端明确指定繁体代码 (LibreTranslate 标准 zt, zh-tw, zh-hant, zh-hk)
	if t == "zt" || t == "zh-hant" || t == "zh-tw" || t == "zh-hk" || t == "traditional" {
		return "zh-Hant"
	}

	// 2. 若客户端明确指定简体代码 (zh-hans, zh-cn, chs)
	if t == "zh-hans" || t == "zh-cn" || t == "chs" {
		return "zh-Hans"
	}

	// 3. 若客户端仅传入宽泛的 "zh" 或未传（依赖默认配置）
	if t == "zh" || t == "" {
		c.mu.RLock()
		def := strings.ToLower(strings.TrimSpace(c.ChineseTargetDefault))
		c.mu.RUnlock()
		if def == "traditional" || def == "zt" || def == "zh-hant" || def == "zh-tw" {
			return "zh-Hant"
		}
		return "zh-Hans"
	}

	// 4. 其他语言直接原样返回（如 en, ja, ko, fr, de 等）
	return t
}

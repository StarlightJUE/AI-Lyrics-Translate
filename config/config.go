package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// DefaultConfigYAML 当完全找不到配置文件时自动生成的标准模版内容
const DefaultConfigYAML = `# ==============================================================
# AI-Lyrics-Translate 配置文件 (config.yaml)
# ==============================================================

# 服务基础设置
server:
  host: "0.0.0.0"       # 监听地址
  port: "5000"          # 监听端口 (兼容 LibreTranslate 默认 5000)

# 大模型 API 设置 (兼容 OpenAI / DeepSeek / SiliconFlow / Ollama 等标准协议)
llm:
  base_url: "https://api.deepseek.com"  # API 根地址
  api_key: ""                           # 你的大模型 API 密钥 (例如 sk-...)
  model: "deepseek-flash"               # 默认调用的模型名称 (如 deepseek-flash / deepseek-chat)
  temperature: 0.2                      # 采样温度 (0.0 ~ 1.0)
  timeout_seconds: 60                   # 单次请求超时时间（秒）
  max_retries: 2                        # 失败最大重试次数

# 翻译与提示词设置
translation:
  # 当客户端仅传入泛指的 "zh" 时，默认输出简体还是繁体：
  # 可选值: simplified (简体中文) / traditional (繁体中文)
  # 注：若客户端明确传入了 zt 或 zh-Hant，系统会自动识别并翻译为繁体中文。
  chinese_target_default: "simplified"
  
  # 提示词版本标识（当提示词重大更新时，修改此项可区分缓存）
  prompt_version: "v1.0"
  
  # 自定义系统提示词（可选，留空则默认使用系统内置的高品质文学级歌词翻译Prompt）
  # 支持占位符 {target} 代表目标语言
  custom_prompt: ""

# 本地 SQLite 缓存数据库设置
storage:
  db_path: "lyrics_cache.db"

# 日志级别 (debug / info / warn / error)
log_level: "info"
`

// FileConfig 对应 YAML / JSON 配置文件模型
type FileConfig struct {
	Server struct {
		Host string `yaml:"host" json:"host"`
		Port string `yaml:"port" json:"port"`
	} `yaml:"server" json:"server"`
	LLM struct {
		BaseURL        string  `yaml:"base_url" json:"base_url"`
		APIKey         string  `yaml:"api_key" json:"api_key"`
		Model          string  `yaml:"model" json:"model"`
		Temperature    float64 `yaml:"temperature" json:"temperature"`
		TimeoutSeconds int     `yaml:"timeout_seconds" json:"timeout_seconds"`
		MaxRetries     int     `yaml:"max_retries" json:"max_retries"`
	} `yaml:"llm" json:"llm"`
	Translation struct {
		ChineseTargetDefault string `yaml:"chinese_target_default" json:"chinese_target_default"`
		PromptVersion        string `yaml:"prompt_version" json:"prompt_version"`
		CustomPrompt         string `yaml:"custom_prompt" json:"custom_prompt"`
	} `yaml:"translation" json:"translation"`
	Lyrics struct {
		ChineseTargetDefault string `yaml:"chinese_target_default" json:"chinese_target_default"`
		PromptVersion        string `yaml:"prompt_version" json:"prompt_version"`
		CustomPrompt         string `yaml:"custom_prompt" json:"custom_prompt"`
	} `yaml:"lyrics" json:"lyrics"`
	Storage struct {
		DBPath string `yaml:"db_path" json:"db_path"`
	} `yaml:"storage" json:"storage"`
	LogLevel string `yaml:"log_level" json:"log_level"`
}

// Config 存储服务运行时全局配置
type Config struct {
	ConfigSource         string // 记录配置来源（如 config.yaml / .env 等）
	ServerPort           string
	ServerHost           string
	LLMBaseURL           string
	LLMAPIKey            string
	LLMModel             string
	LLMTemperature       float64
	LLMTimeoutSeconds    int
	LLMMaxRetries        int
	PromptVersion        string
	CustomPrompt         string
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

	// 尝试加载 .env 垫底（兼容容器化与环境变量）
	for _, envPath := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(envPath); err == nil {
			_ = godotenv.Load(envPath)
			break
		}
	}

	cfg := &Config{
		ServerPort:           "5000",
		ServerHost:           "0.0.0.0",
		LLMBaseURL:           "https://api.deepseek.com",
		LLMAPIKey:            "",
		LLMModel:             "deepseek-flash",
		LLMTemperature:       0.2,
		LLMTimeoutSeconds:    60,
		LLMMaxRetries:        2,
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
		// 检查当前目录下是否存在 .env
		hasEnv := false
		for _, ep := range []string{".env", "../.env"} {
			if _, err := os.Stat(ep); err == nil {
				hasEnv = true
				cfg.ConfigSource = ep
				break
			}
		}

		// 若既没有 config.yaml 也没有 .env，自动在当前目录创建 config.yaml 方便用户直接修改
		if !hasEnv && targetPath == "" {
			const autoPath = "config.yaml"
			if err := os.WriteFile(autoPath, []byte(DefaultConfigYAML), 0644); err == nil {
				cfg.ConfigSource = autoPath + " (首次运行自动创建)"
				_ = loadFromFile(autoPath, cfg)
				fmt.Println("[提示] 检测到首次运行，已自动生成默认配置文件 config.yaml，请填入你的 API Key 后启动！")
			}
		}
	}

	// 2. 允许系统环境变量覆盖配置文件中的对应项（满足容器化部署需求）
	overrideWithEnv(cfg)

	return cfg
}

func findConfigFile(specified string) string {
	if specified != "" {
		if _, err := os.Stat(specified); err == nil {
			return specified
		}
		log.Printf("[WARN] 指定的配置文件路径不存在: %s", specified)
	}

	// 候选路径搜索
	var candidates []string

	// 当前工作目录
	candidates = append(candidates, "config.yaml", "config.yml", "config.json")

	// 可执行文件所在同级目录（防止用户通过快捷方式或在其他路径双击 exe 时找不到配置）
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "config.yaml"),
			filepath.Join(exeDir, "config.yml"),
			filepath.Join(exeDir, "config.json"),
		)
	}

	// 单元测试目录上探
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
		// 默认按 YAML 解析
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

	if f.LLM.BaseURL != "" {
		cfg.LLMBaseURL = f.LLM.BaseURL
	}
	if f.LLM.APIKey != "" {
		cfg.LLMAPIKey = f.LLM.APIKey
	}
	if f.LLM.Model != "" {
		cfg.LLMModel = f.LLM.Model
	}
	if f.LLM.Temperature > 0 {
		cfg.LLMTemperature = f.LLM.Temperature
	}
	if f.LLM.TimeoutSeconds > 0 {
		cfg.LLMTimeoutSeconds = f.LLM.TimeoutSeconds
	}
	if f.LLM.MaxRetries > 0 {
		cfg.LLMMaxRetries = f.LLM.MaxRetries
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
	if val := os.Getenv("LLM_BASE_URL"); val != "" {
		cfg.LLMBaseURL = val
	}
	if val := os.Getenv("LLM_API_KEY"); val != "" {
		cfg.LLMAPIKey = val
	}
	if val := os.Getenv("LLM_MODEL"); val != "" {
		cfg.LLMModel = val
	}
	if val := os.Getenv("LLM_TEMPERATURE"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.LLMTemperature = f
		}
	}
	if val := os.Getenv("LLM_TIMEOUT_SECONDS"); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			cfg.LLMTimeoutSeconds = i
		}
	}
	if val := os.Getenv("LLM_MAX_RETRIES"); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			cfg.LLMMaxRetries = i
		}
	}
	if val := os.Getenv("PROMPT_VERSION"); val != "" {
		cfg.PromptVersion = val
	}
	if val := os.Getenv("CUSTOM_PROMPT"); val != "" {
		cfg.CustomPrompt = val
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
		def := strings.ToLower(strings.TrimSpace(c.ChineseTargetDefault))
		if def == "traditional" || def == "zt" || def == "zh-hant" || def == "zh-tw" {
			return "zh-Hant"
		}
		return "zh-Hans"
	}

	// 4. 其他语言直接原样返回（如 en, ja, ko, fr, de 等）
	return t
}

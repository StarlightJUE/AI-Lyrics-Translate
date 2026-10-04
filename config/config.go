package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config 存储全局服务配置
type Config struct {
	ServerPort        string
	ServerHost        string
	LLMBaseURL        string
	LLMAPIKey         string
	LLMModel          string
	LLMTemperature    float64
	LLMTimeoutSeconds int
	LLMMaxRetries     int
	PromptVersion        string
	CustomPrompt         string
	ChineseTargetDefault string // simplified (默认) 或 traditional
	DBPath               string
	LogLevel             string
}

// LoadConfig 从 .env 文件和环境变量中加载配置，保证密钥隔离与安全
func LoadConfig() *Config {
	// 尝试加载本地及上级目录中的 .env 文件
	for _, envPath := range []string{".env", "../.env", "../../.env"} {
		if _, err := os.Stat(envPath); err == nil {
			_ = godotenv.Load(envPath)
			break
		}
	}

	cfg := &Config{
		ServerPort:           getEnv("SERVER_PORT", "5000"),
		ServerHost:           getEnv("SERVER_HOST", "0.0.0.0"),
		LLMBaseURL:           getEnv("LLM_BASE_URL", "https://api.deepseek.com"),
		LLMAPIKey:            getEnv("LLM_API_KEY", ""),
		LLMModel:             getEnv("LLM_MODEL", "deepseek-flash"),
		LLMTemperature:       getEnvFloat("LLM_TEMPERATURE", 0.2),
		LLMTimeoutSeconds:    getEnvInt("LLM_TIMEOUT_SECONDS", 60),
		LLMMaxRetries:        getEnvInt("LLM_MAX_RETRIES", 2),
		PromptVersion:        getEnv("PROMPT_VERSION", "v1.0"),
		CustomPrompt:         getEnv("CUSTOM_PROMPT", ""),
		ChineseTargetDefault: getEnv("CHINESE_TARGET_DEFAULT", "simplified"),
		DBPath:               getEnv("DB_PATH", "lyrics_cache.db"),
		LogLevel:             getEnv("LOG_LEVEL", "info"),
	}

	return cfg
}

// ResolveTargetLanguage 根据客户端传入的 target 与 env 默认配置，精准解析简繁体及通用语言代码
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

	// 3. 若客户端仅传入宽泛的 "zh" 或未传（依赖环境默认配置）
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

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}

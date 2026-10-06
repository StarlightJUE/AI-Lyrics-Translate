package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-lyrics-translate/config"
	"ai-lyrics-translate/llm"
	"ai-lyrics-translate/server"
	"ai-lyrics-translate/storage"
)

const AppVersion = "v1.0.0"

func main() {
	// 命令行参数解析
	var configPath string
	var showVersion bool

	flag.StringVar(&configPath, "config", "", "指定配置文件路径 (例如 config.yaml 或 config.json)")
	flag.StringVar(&configPath, "c", "", "指定配置文件路径简写")
	flag.BoolVar(&showVersion, "version", false, "显示程序版本信息")
	flag.BoolVar(&showVersion, "v", false, "显示程序版本信息简写")
	flag.Parse()

	if showVersion {
		fmt.Printf("AI-Lyrics-Translate %s\n", AppVersion)
		return
	}

	// 1. 加载配置（优先级：-c 指定文件 > config.yaml/json > .env > 环境变量 > 自动生成）
	cfg := config.LoadConfig(configPath)

	fmt.Println("==================================================")
	fmt.Printf("   AI-Lyrics-Translate 歌词翻译本地中继缓存 (%s)\n", AppVersion)
	fmt.Println("==================================================")
	fmt.Printf("协议兼容: LibreTranslate REST API (/translate)\n")
	fmt.Printf("配置来源: %s\n", cfg.ConfigSource)
	fmt.Printf("监听地址: http://%s:%s\n", cfg.ServerHost, cfg.ServerPort)
	fmt.Printf("默认模型: %s (BaseURL: %s)\n", cfg.LLMModel, cfg.LLMBaseURL)
	fmt.Printf("提示词版本: %s\n", cfg.PromptVersion)
	if cfg.CustomPrompt != "" {
		fmt.Println("提示词模式: 自定义 PROMPT (CUSTOM_PROMPT)")
	} else {
		fmt.Println("提示词模式: 内置调优 PROMPT (文学词作增强版)")
	}
	fmt.Printf("简繁体偏好: %s\n", cfg.ChineseTargetDefault)
	fmt.Printf("缓存数据库: %s (SQLite WAL Mode)\n", cfg.DBPath)
	if cfg.LLMAPIKey == "" || cfg.LLMAPIKey == "your_api_key_here" {
		fmt.Printf("[警告] 当前未配置有效 API Key，请在 %s 中配置 llm.api_key！\n", cfg.ConfigSource)
	} else {
		fmt.Printf("API Key 状态: 已配置 (前缀: %s...)\n", maskKey(cfg.LLMAPIKey))
	}
	fmt.Println("==================================================")

	// 2. 初始化本地 SQLite 缓存存储
	store, err := storage.InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer store.Close()

	// 3. 初始化 LLM 客户端与 Server
	llmClient := llm.NewClient(cfg)
	srvInstance := server.NewServer(cfg, store, llmClient)

	mux := http.NewServeMux()
	srvInstance.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", cfg.ServerHost, cfg.ServerPort),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // 容纳大模型批量翻译可能带来的耗时
		IdleTimeout:  120 * time.Second,
	}

	// 4. 优雅关闭监听
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("服务启动成功，等待客户端请求...")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务运行异常: %v", err)
		}
	}()

	<-stopChan
	log.Println("\n接收到关闭信号，正在安全停止服务并落盘缓存...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("服务关闭出错: %v", err)
	}
	srvInstance.Close()
	log.Println("服务已完全退出。")
}

func maskKey(key string) string {
	if len(key) <= 6 {
		return "******"
	}
	return key[:6]
}

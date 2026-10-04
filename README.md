# AI-Lyrics-Translate 🎵

> 针对音乐播放器与歌词插件深度优化的 **AI 歌词文学翻译与本地缓存中继服务**。  
> 对外提供标准的 **LibreTranslate REST API** 规范，开箱即用无缝接入主流音乐播放器！

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Protocol](https://img.shields.io/badge/Protocol-LibreTranslate-green)](https://libretranslate.com)
[![Cache](https://img.shields.io/badge/Cache-SQLite%20WAL-orange)](https://sqlite.org)
[![License](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)

---

## 🌟 核心特性与设计亮点

1. **协议完全兼容 LibreTranslate**：
   - 暴露标准 `POST /translate`、`GET /languages`、`GET /frontend/settings` 等端点。
   - 主流支持自定义 LibreTranslate 地址的播放器与插件（如 YesPlayMusic、Lyricify、桌面歌词工具等）**无需任何插件修改，填入本地地址直接可用**。

2. **内容感知哈希（SHA-256）与 SQLite 本地缓存**：
   - **0 API 消耗**：听过的歌词毫秒级秒出，API 调用成本直接降到最低。
   - 启用 SQLite **WAL (Write-Ahead Logging)** 高性能并发模式，断电落盘不丢数据。
   - 缓存带有模型版本与 Prompt 版本标识，方便后续无损平滑升级。

3. **智能时间轴剥离与 1:1 本地重组（独创降本优化）**：
   - **Token 暴省 30%~50%**：本地自动剥离 LRC/TTML 时间戳标签（如 `[mm:ss.xx]`、`<mm:ss.xx>`），只将格式化后的纯文本歌词喂给大模型。
   - **彻底告别时间轴损坏**：大模型不需要费力理解与复刻时间戳，翻译完毕后由本地程序 1:1 严密拼回原始时间轴，毫秒不错位。

4. **专属歌词文学级 Prompt（繁简智能处理）**：
   - 针对歌词意境特别调优，语言典雅考究，符合中文流行音乐与诗意审美。
   - 若原歌词为繁体中文，指令将严格执行繁转简字符映射，保证原词原意不被篡改。

5. **极致轻量与真正绿色单文件（~10MB 内存）**：
   - 基于纯 Go 开发（内置纯 Go SQLite 驱动，无 CGO 依赖）。
   - 空闲常驻内存仅 **约 8MB ~ 15MB**，开机后台静默运行毫无负担。
   - 跨平台交叉编译：可以在 Windows 上一键编译出 Linux、macOS 单文件。

6. **严格的 API 隔离与开源安全性**：
   - 敏感 Key、自定义 URL 全部收拢于 `.env` 环境变量，内置 `.gitignore`，开源绝无泄露风险。

---

## 🚀 快速上手

### 1. 配置准备

克隆项目后，从示例模版创建本地配置文件：

```bash
cp .env.example .env
```

编辑 `.env` 文件，填入你的大模型 API 配置（以 DeepSeek 为例）：

```ini
# 服务监听端口与地址
SERVER_PORT=5000
SERVER_HOST=0.0.0.0

# LLM 配置 (兼容 OpenAI / DeepSeek / SiliconFlow / Ollama 等标准协议)
LLM_BASE_URL=https://api.deepseek.com
LLM_API_KEY=sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
LLM_MODEL=deepseek-flash
LLM_TEMPERATURE=0.2
LLM_TIMEOUT_SECONDS=60
LLM_MAX_RETRIES=2

# 缓存与数据库
DB_PATH=lyrics_cache.db
PROMPT_VERSION=v1.0

# 可选：自定义系统提示词（留空则默认使用系统内置的高品质歌词翻译Prompt）
# CUSTOM_PROMPT=你是一位专业的歌词翻译家...

# 可选：当客户端仅传入泛指的目标语言 "zh" 时，默认输出简体还是繁体 (simplified / traditional)
CHINESE_TARGET_DEFAULT=simplified
```

### 2. 本地直接运行

```bash
# 运行服务
go run main.go
```

启动后，控制台将输出服务就绪信息：
```
==================================================
   AI-Lyrics-Translate 歌词翻译本地中继缓存服务   
==================================================
协议兼容: LibreTranslate REST API (/translate)
监听地址: http://0.0.0.0:5000
默认模型: deepseek-flash (BaseURL: https://api.deepseek.com)
提示词版本: v1.0
缓存数据库: lyrics_cache.db (SQLite WAL Mode)
API Key 状态: 已配置 (前缀: sk-af9...)
==================================================
```

---

## 🎧 播放器与客户端接入指南

由于完全兼容 LibreTranslate 规范，只需在支持 LibreTranslate 的播放器设置中填入本服务地址即可：

| 设置项 | 推荐填入值 | 说明 |
| :--- | :--- | :--- |
| **翻译引擎类型** | `LibreTranslate` | 选择 LibreTranslate 协议 |
| **API 端点 / 服务器地址** | `http://127.0.0.1:5000` | 本地服务监听地址 |
| **API 密钥 (API Key)** | *(留空即可)* | 本地服务已配置好密钥转发，无需客户端二次鉴权 |
| **目标语言** | `zh` (Chinese) | 简体中文 |

> **提示**：若将本服务部署在家庭 NAS、群晖或 Linux 软路由上，播放器中的服务器地址填入 NAS 的局域网 IP（例如 `http://192.168.1.100:5000`）即可全屋设备共享缓存！

---

## 🛠️ 编译与跨平台发布

本项目使用纯 Go 实现 SQLite 引擎（`CGO_ENABLED=0`），可在任意操作系统上一键交叉编译其他平台的独立二进制：

### 编译 Windows 绿色单文件 (`.exe`)
```powershell
go build -ldflags="-s -w" -o AI-Lyrics-Translate.exe .
```

### 在 Windows 上一键交叉编译 Linux 版本（丢给 VPS / 群晖 / Docker）
```powershell
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o bin/ai-lyrics-translate-linux .
```

### 在 Windows 上编译 macOS 版本 (Apple Silicon M1/M2/M3)
```powershell
$env:CGO_ENABLED="0"; $env:GOOS="darwin"; $env:GOARCH="arm64"; go build -ldflags="-s -w" -o bin/ai-lyrics-translate-darwin .
```

---

## 📡 接口列表 (API Specifications)

### 1. 翻译接口 `POST /translate`

- **请求格式**：支持 `application/json` 与 `application/x-www-form-urlencoded`
- **请求体 (JSON)**：
  ```json
  {
    "q": "[00:01.00]When I was young\n[00:04.50]I listened to the radio",
    "source": "auto",
    "target": "zh",
    "format": "text"
  }
  ```
- **响应体 (JSON)**：
  ```json
  {
    "translatedText": "[00:01.00] 当我年少时\n[00:04.50] 我常常倾听收音机"
  }
  ```

### 2. 语言列表 `GET /languages`
返回支持的目标语言列表与探测标识。

### 3. 健康检查 `GET /health` 或 `GET /`
查看服务版本、当前使用的模型与 SQLite 缓存就绪状态。

---

## 📄 License
 
本项目基于 [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE) 开源。

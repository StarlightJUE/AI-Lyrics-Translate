# AI-Lyrics-Translate 🎵

> 针对音乐播放器与歌词插件深度优化的 **AI 歌词文学翻译与本地缓存中继服务**。  
> 对外提供标准的 **LibreTranslate REST API** 规范，开箱即用无缝接入主流音乐播放器！

[![Release](https://img.shields.io/github/v/release/StarlightJUE/AI-Lyrics-Translate?color=brightgreen&label=Release)](https://github.com/StarlightJUE/AI-Lyrics-Translate/releases)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Protocol](https://img.shields.io/badge/Protocol-LibreTranslate-green)](https://libretranslate.com)
[![Cache](https://img.shields.io/badge/Cache-SQLite%20WAL-orange)](https://sqlite.org)
[![License](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)

---

## 🌟 核心特性与设计亮点

1. **协议完全兼容 LibreTranslate**：
   - 暴露标准 `POST /translate`、`GET /languages`、`GET /frontend/settings` 等端点。
   - 主流支持自定义 LibreTranslate 地址的播放器与插件（如 YesPlayMusic、Lyricify、桌面歌词工具等）**无需任何插件修改，填入本地地址直接可用**。

2. **开箱即用，完全依赖配置文件 (`config.yaml`)**：
   - 提供开箱即用的结构化配置文件支持，全平台统一样式。
   - **首次启动智能初始化**：若未检测到配置文件，程序自动在当前目录下生成带详尽中文注释的标准 `config.yaml`，填入 Key 即可启动。
   - 支持多平台绿色单文件发布，无需安装任何外部运行库。

3. **内容感知哈希（SHA-256）与 SQLite 本地缓存**：
   - **0 API 消耗**：听过的歌词毫秒级秒出，API 调用成本直接降到最低。
   - 启用 SQLite **WAL (Write-Ahead Logging)** 高性能并发模式，断电落盘不丢数据。
   - 缓存带有模型版本与 Prompt 版本标识，方便后续平滑迁移。

4. **智能时间轴剥离与 1:1 本地重组（独创降本优化）**：
   - **Token 暴省 30%~50%**：本地自动剥离 LRC/TTML 时间戳标签（如 `[mm:ss.xx]`、`<mm:ss.xx>`），只将格式化后的纯文本歌词喂给大模型。
   - **彻底告别时间轴错位**：大模型不需要费力理解与复刻时间戳，翻译完毕后由本地程序 1:1 严密拼回原始时间轴，毫秒不错位。

5. **专属歌词文学级 Prompt（繁简智能处理）**：
   - 针对歌词意境特别调优，语言典雅考究，拒绝机械直译与干瘪白话。
   - **语种自适应**：智能忽略客户端错误的源语言误报（如部分播放器将小语种误标为 `en`），大模型自行判断源语种。
   - **中文原文保护**：若原歌词为繁体中文，严格执行繁转简字符映射，保证原词原意不被篡改。

6. **极致轻量（~10MB 内存）与纯 Go 实现**：
   - 基于纯 Go 开发（内置纯 Go SQLite 驱动，无 CGO 依赖）。
   - 空闲常驻内存仅 **约 8MB ~ 15MB**，开机后台静默运行毫无负担。
   - 严格安全隔离：敏感 Key 永不入库，默认仅随配置文件保存在本地。

---

## 🚀 快速上手

### 方式一：直接下载发布包（推荐）

1. 前往 👉 [**GitHub Releases**](https://github.com/StarlightJUE/AI-Lyrics-Translate/releases) 页面，下载对应系统的预编译包：
   - **Windows**: `ai-lyrics-translate-windows-amd64.zip`
   - **Linux**: `ai-lyrics-translate-linux-amd64.tar.gz` (或 arm64)
   - **macOS**: `ai-lyrics-translate-darwin-arm64.tar.gz` (Apple Silicon) / `darwin-amd64.tar.gz` (Intel)
2. 解压到任意目录。
3. **首次运行**：
   - **Windows**：直接双击 `ai-lyrics-translate.exe`。
   - **Linux / macOS**：在终端中执行 `./ai-lyrics-translate`。
   - 程序检测到没有配置文件时，会在同目录下**自动生成**标准的 `config.yaml` 文件。
4. 使用文本编辑器打开 `config.yaml`，填入你的 API Key（例如 DeepSeek 密钥）：
   ```yaml
   llm:
     api_key: "sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
   ```
5. 再次运行程序，看到服务就绪提示后即可！

---

### 方式二：从源码编译 / 运行

```bash
# 1. 克隆仓库
git clone https://github.com/StarlightJUE/AI-Lyrics-Translate.git
cd AI-Lyrics-Translate

# 2. 复制配置模版
cp config.example.yaml config.yaml

# 3. 编辑 config.yaml 填入你的大模型 API 密钥
# 4. 运行服务
go run main.go
```

启动成功后，控制台将显示服务就绪看板：
```text
==================================================
   AI-Lyrics-Translate 歌词翻译本地中继缓存 (v1.0.0)
==================================================
协议兼容: LibreTranslate REST API (/translate)
配置来源: config.yaml
监听地址: http://0.0.0.0:5000
默认模型: deepseek-flash (BaseURL: https://api.deepseek.com)
提示词版本: v1.0
提示词模式: 内置调优 PROMPT (文学词作增强版)
简繁体偏好: simplified
缓存数据库: lyrics_cache.db (SQLite WAL Mode)
API Key 状态: 已配置 (前缀: sk-af9...)
==================================================
```

---

## ⚙️ 配置文件说明 (`config.yaml`)

服务完全依赖清晰结构化的 `config.yaml`，完整配置项如下：

```yaml
# ==============================================================
# AI-Lyrics-Translate 配置文件 (config.yaml)
# ==============================================================

# 服务基础设置
server:
  host: "0.0.0.0"       # 监听地址 (局域网共享请保持 0.0.0.0)
  port: "5000"          # 监听端口 (兼容 LibreTranslate 默认 5000)

# 大模型 API 设置 (兼容 OpenAI / DeepSeek / SiliconFlow / Ollama 等标准协议)
llm:
  base_url: "https://api.deepseek.com"  # API 根地址
  api_key: "sk-xxxxxxxxxxxxxxxx"        # 你的大模型 API 密钥
  model: "deepseek-flash"               # 推荐 deepseek-flash（速度极快、成本极低）
  temperature: 0.2                      # 采样温度 (0.0 ~ 1.0)
  timeout_seconds: 60                   # 单次请求超时时间（秒）
  max_retries: 2                        # 失败最大重试次数

# 翻译与提示词设置
translation:
  # 当客户端仅传入泛指的 "zh" 时，默认输出简体还是繁体：
  # 可选值: simplified (简体中文) / traditional (繁体中文)
  chinese_target_default: "simplified"
  
  # 提示词版本标识（当提示词重大更新时，修改此项可自动隔离旧缓存）
  prompt_version: "v1.0"
  
  # 自定义系统提示词（可选，留空则默认使用系统内置的高品质文学级歌词翻译Prompt）
  # custom_prompt: ""

# 本地 SQLite 缓存数据库设置
storage:
  db_path: "lyrics_cache.db"

# 日志级别 (debug / info / warn / error)
log_level: "info"
```

> **提示**：除了 `config.yaml`，系统亦兼容通过命令行参数指定配置（`-c /path/to/custom_config.yaml`），在 Docker 部署中也支持直接使用环境变量进行参数覆盖。

---

## 🎧 播放器与客户端接入指南

由于完全兼容 LibreTranslate 规范，只需在支持 LibreTranslate 的播放器设置中填入本服务地址即可：

| 设置项 | 推荐填入值 | 说明 |
| :--- | :--- | :--- |
| **翻译引擎类型** | `LibreTranslate` | 选择 LibreTranslate 协议 |
| **API 端点 / 服务器地址** | `http://127.0.0.1:5000` | 本地服务监听地址 |
| **API 密钥 (API Key)** | *(留空即可)* | 本地服务已配置好密钥转发，无需客户端二次鉴权 |
| **目标语言** | `zh` (Chinese) | 简体中文 |

> **进阶玩法**：若将本服务部署在家庭 NAS、群晖、微型软路由或 Linux 服务器上，播放器中的服务器地址填入该设备的局域网 IP（例如 `http://192.168.1.100:5000`），即可全屋手机、电脑等多设备共享歌词翻译缓存，体验秒开！

---

## 🛠️ 编译与跨平台发布

本项目使用纯 Go 实现 SQLite 引擎（`CGO_ENABLED=0`），可在任意操作系统上一键交叉编译：

### 编译 Windows 绿色单文件
```powershell
go build -ldflags="-s -w" -o ai-lyrics-translate.exe .
```

### 交叉编译 Linux 版本（丢给 VPS / 群晖 / NAS）
```powershell
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o ai-lyrics-translate-linux .
```

### 交叉编译 macOS 版本 (Apple Silicon)
```powershell
$env:CGO_ENABLED="0"; $env:GOOS="darwin"; $env:GOARCH="arm64"; go build -ldflags="-s -w" -o ai-lyrics-translate-darwin .
```

---

## 📡 接口列表 (API Specifications)

### 1. 翻译接口 `POST /translate`

- **请求格式**：支持 `application/json` 与 `application/x-www-form-urlencoded`
- **请求体 (JSON 示例)**：
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
    "translatedText": "[00:01.00] 当我年少轻狂时\n[00:04.50] 曾终日聆听着电波里的歌声"
  }
  ```

### 2. 语言列表 `GET /languages`
返回系统支持的目标语言列表与探测标识。

### 3. 健康检查 `GET /health` 或 `GET /`
查看服务版本、当前使用的模型与 SQLite 缓存就绪状态。

---

## 📄 License
 
本项目基于 [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE) 开源。

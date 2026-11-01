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

2. **内置开箱即用的现代化 Web 单页面控制台**：
   - 在浏览器中直接访问 `http://localhost:5000/` 或 `http://localhost:5000/ui` 即可打开可视化管理后台！
   - **歌词缓存管理**：直观查看已缓存歌曲，支持即时搜索、单条删除或一键清空全部缓存。
   - **手动在线修改歌词**：支持在线查看原文与译文对照，手动修改歌词后一键落盘保存到 SQLite，下一次切歌直接秒出修改后的歌词。
   - **提示词管理**：元提示词与用户提示词拆分，前端可实时在线调整用户审美提示词并热保存生效。
   - **模型优先级一键切换**：支持直观查看 DeepSeek 与 Gemini 的运行状态，一键切换主备调用优先级。

3. **双路大模型支持（DeepSeek & Gemini）+ 自动故障切换 (Failover)**：
   - 支持主流 **DeepSeek 第三方/官方 OpenAI 兼容接口** 与 **Google Gemini 官方 REST / 第三方兼容接口**。
   - 允许用户在配置或网页控制台中自由指定调用优先级（例如首选 DeepSeek，备选 Gemini）。
   - 当首选模型发生网络超时、服务不可用 (5xx)、429 配额耗尽时，系统**毫秒级自动降级切换至下一个模型**继续翻译，确保听歌体验丝滑不断流。

4. **歌名与歌手上下文感知（智能意境理解）**：
   - 解析 LRC、ASS、WebVTT 时自动捕获歌名与歌手信息（如 `[ti:晴天]`, `[ar:周杰伦]`）。
   - 喂给大模型时自动附带上下文背景提示（`【背景信息】歌曲名：《{title}》，歌手/乐队：{artist}`），让大模型充分理解歌曲的创作背景与情感张力。
   - **元提示词硬编码约束**：系统元提示词严格限制大模型“严禁在输出中包含背景信息”，确保返回的歌词行数与格式 1:1 纯净如初。

5. **日韩语转罗马音 (Romaji / Romanization) 支持**：
   - 全面支持日语歌词转赫本式罗马字（Hepburn Romaji）以及韩语歌词转文观部罗马音（Revised Romanization of Korean）。
   - **按需触发**：具体转不转完全听从客户端请求，客户端在请求中带上 `?romaji=true`、`?romanize=1`、`target: "romaji"` 或 HTTP Header `X-Lyrics-Romanize: true` 即可无缝切换为罗马音注音模式，且独立缓存互不干扰。

6. **全格式适配与智能时间轴剥离（独创降本优化）**：
   - **全格式支持**：全面支持标准 LRC、Enhanced LRC、ESLRC、ASS/SSA (Advanced SubStation Alpha)、WebVTT、SRT 以及 TTML。
   - **Token 暴省 30%~50%**：本地自动剥离时间戳标签与逐字卡拉OK特效（如 `<mm:ss.xx>`、`(\d+,\d+)`、`{\k...}`），拼回时自动降维保留逐行时间轴，只将纯净文本歌词喂给大模型。
   - **彻底告别时间轴错位**：大模型无需理解或复刻复杂时间轴标记，翻译完毕后由本地程序 1:1 严密拼回原始时间轴，毫秒不错位。

7. **切歌并发保护与后台解耦缓存（独创切歌抗损优化）**：
   - **切歌不丢缓存**：用户在播放器中快速切歌导致 HTTP 请求被主动取消时，只要该歌词已派发至大模型，系统便会自动脱离客户端生命周期转入后台独立运行至完毕，并完整持久化写入 SQLite。下次再切回该歌曲时直接毫秒级秒出，彻底杜绝二次 MISS 与重复消耗 Token。
   - **并发去重复用 (In-Flight Dedup)**：当播放器主界面与桌面歌词组件同时发起相同翻译请求时，自动复用在途任务合并等待，避免重复计费。

8. **极致轻量（~15MB 内存）与纯 Go 实现**：
   - 基于纯 Go 开发（内置纯 Go SQLite 驱动，无 CGO 依赖）。
   - 空闲常驻内存仅 **约 10MB ~ 15MB**，开机后台静默运行毫无负担。
   - 严格安全隔离：敏感 Key 永不入库，默认仅保存在本地配置文件中。

---

## 🚀 快速上手

### 方式一：直接下载预编译二进制

1. 前往 👉 [**GitHub Releases**](https://github.com/StarlightJUE/AI-Lyrics-Translate/releases) 页面，下载对应系统的预编译包。
2. 解压到任意目录并启动程序（Windows 双击 `ai-lyrics-translate.exe`）。
3. 程序首次运行会自动生成带详细中文注释的 `config.yaml`。
4. 打开浏览器访问 `http://localhost:5000/`，在 Web 界面中直接查看与管理！

---

### 方式二：从源码运行

```bash
# 1. 克隆仓库
git clone https://github.com/StarlightJUE/AI-Lyrics-Translate.git
cd AI-Lyrics-Translate

# 2. 复制配置模版
cp config.example.yaml config.yaml

# 3. 运行服务
go run main.go
```

启动成功后，控制台将显示服务看板：
```text
==================================================
   AI-Lyrics-Translate 歌词翻译本地中继缓存 (v1.1.0)
==================================================
协议兼容: LibreTranslate REST API (/translate)
Web 管理面板: http://0.0.0.0:5000/ (或 /ui)
配置来源: config.yaml
模型优先级: [deepseek gemini]
当前首选: deepseek-chat (BaseURL: https://api.deepseek.com)
提示词版本: v1.0
提示词模式: 内置文学级调优 PROMPT (元提示词骨架保护)
简繁体偏好: simplified
日韩罗马音: 已就绪 (客户端请求附带 ?romaji=true 即可触发)
缓存数据库: lyrics_cache.db (SQLite WAL Mode)
==================================================
```

---

## ⚙️ 配置文件说明 (`config.yaml`)

```yaml
# ==============================================================
# AI-Lyrics-Translate 配置文件 (config.yaml)
# ==============================================================

# 服务基础设置
server:
  host: "0.0.0.0"       # 监听地址 (局域网共享请保持 0.0.0.0)
  port: "5000"          # 监听端口 (兼容 LibreTranslate 默认 5000)

# 双路大模型 API 设置 (支持 DeepSeek 与 Gemini，支持手动指定优先级与故障自动切换)
llm:
  # 优先调用顺序列表：首个不可用时自动顺延切换下一个
  priority:
    - "deepseek"
    - "gemini"

  # 提供商详细配置
  providers:
    # 1. DeepSeek 官方或各类第三方 OpenAI 兼容端点 (SiliconFlow / 中转站 / Ollama 等)
    deepseek:
      type: "openai"                       # 协议类型: openai
      base_url: "https://api.deepseek.com"  # 官方或第三方 API 端点
      api_key: "sk-xxxxxxxx"                # 你的 API 密钥
      model: "deepseek-chat"                # 模型名称
      temperature: 0.2                      # 采样温度
      timeout_seconds: 60                   # 单次请求超时时间（秒）
      max_retries: 2                        # 失败最大重试次数

    # 2. Google Gemini 官方原生端点 / 第三方兼容端点
    gemini:
      type: "gemini"                       # 协议类型: gemini (官方 REST) 或 openai (第三方兼容)
      base_url: "https://generativelanguage.googleapis.com/v1beta"
      api_key: "AIzaxxxxxxxx"               # Google Gemini API 密钥
      model: "gemini-2.0-flash"             # 模型名称
      temperature: 0.2
      timeout_seconds: 60
      max_retries: 2

# 翻译与提示词设置
translation:
  chinese_target_default: "simplified"     # 默认简繁体: simplified / traditional
  prompt_version: "v1.0"                   # 提示词版本标识
  custom_prompt: ""                        # 用户自定义提示词（留空使用系统默认文学级歌词调优）

# 本地 SQLite 缓存数据库设置
storage:
  db_path: "lyrics_cache.db"

# 日志级别 (debug / info / warn / error)
log_level: "info"
```

---

## 🎧 播放器与客户端接入指南

| 设置项 | 推荐填入值 | 说明 |
| :--- | :--- | :--- |
| **翻译引擎类型** | `LibreTranslate` | 选择 LibreTranslate 协议 |
| **API 端点 / 服务器地址** | `http://127.0.0.1:5000` | 本地服务监听地址 |
| **API 密钥 (API Key)** | *(留空即可)* | 本地服务已配置好密钥转发，无需客户端二次鉴权 |
| **目标语言** | `zh` (Chinese) | 简体中文（若需日韩罗马音可传 `romaji`） |

> **提示**：若客户端想要将日韩语歌词转为罗马音，可通过以下任一方式发起请求：
> - URL Query 带上 `?romaji=true` 或 `?romanize=true`；
> - 请求体传 `"target": "romaji"`；
> - 请求 Header 附带 `X-Lyrics-Romanize: true`。

---

## 📡 接口列表 (API Specifications)

### 1. 翻译接口 `POST /translate`
- 支持 JSON 与 Form 表单，自动提取元数据与时间轴，调用大模型并按行拼回。

### 2. Web 管理界面 `GET /` 或 `GET /ui`
- 浏览器打开即为单页面控制台。

### 3. 歌词缓存管理 `GET / PUT / DELETE /api/lyrics`
- `GET /api/lyrics?limit=50&q=关键词`：查询歌词缓存列表与分页。
- `PUT /api/lyrics`：传入 `{"key": "...", "translated_text": "..."}` 在线修改歌词。
- `DELETE /api/lyrics?key=...`：删除指定歌词缓存（`key=all` 清空所有缓存）。

### 4. 系统配置管理 `GET / POST /api/config`
- `GET /api/config`：查看当前提示词与模型优先级。
- `POST /api/config`：保存更新用户提示词或调整模型优先级列表。

---

## 📄 License
 
本项目基于 [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE) 开源。

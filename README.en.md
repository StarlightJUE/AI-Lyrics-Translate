# AI-Lyrics-Translate 🎵

[English](README.en.md) | [简体中文](README.md)

> A lightweight, literary-tuned **AI Lyrics Translation & Local Caching Relay Service** tailored for music players and lyric plugins.  
> Exposes standard **LibreTranslate REST API** endpoints, enabling seamless, out-of-the-box integration with mainstream players!

[![Release](https://img.shields.io/github/v/release/StarlightJUE/AI-Lyrics-Translate?color=brightgreen&label=Release)](https://github.com/StarlightJUE/AI-Lyrics-Translate/releases)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Protocol](https://img.shields.io/badge/Protocol-LibreTranslate-green)](https://libretranslate.com)
[![Cache](https://img.shields.io/badge/Cache-SQLite%20WAL-orange)](https://sqlite.org)
[![License](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](LICENSE)

---

## 🌟 Key Features & Highlights

1. **Fully LibreTranslate Protocol Compatible**:
   - Exposes standard `POST /translate`, `GET /languages`, `GET /frontend/settings`, and `/health` endpoints.
   - Any player or plugin supporting custom LibreTranslate servers (e.g., YesPlayMusic, Lyricify, Desktop Lyric tools) works directly with **zero code modifications**.

2. **Built-in Modern Web Management Console (SPA)**:
   - Access `http://localhost:5000/` or `http://localhost:5000/ui` in your browser.
   - **Cache Management**: Search, preview source and translation, delete single entries, or clear all caches.
   - **Manual Lyric Editing**: Edit translated text directly on the page and save instantly to SQLite.
   - **Prompts Management**: Separate Meta Framework and User Prompts. Edit prompts online and persist immediately.
   - **Failover Chain & Model Config**: Adjust priorities and update Base URL, Model, or API Keys directly in the UI.
   - **Multi-Language (i18n)**: Seamless instant switching between **zh-CN, zh-TW, en, ja, ko**.

3. **Dual-LLM Support (DeepSeek & Gemini) with Automatic Failover**:
   - Supports **DeepSeek (OpenAI-compatible endpoints)** and **Google Gemini (official REST v1beta / 3.5 Flash)**.
   - Prioritized failover: On network timeout, 5xx, or 429 quota exhaustion, it switches smoothly to the next model in milliseconds.

4. **Context-Aware Lyrics (Song Title & Artist)**:
   - Automatically parses `[ti:]` (title) and `[ar:]` (artist) metadata from LRC, ASS, and WebVTT.
   - Injected into LLM context for deeper emotional nuance and accurate cultural references.
   - **Anti-Leakage Meta Constraint**: Hardcoded meta prompt strictly forbids the model from outputting background info, ensuring a pure 1:1 line mapping.

5. **Japanese & Korean Romaji Transliteration**:
   - Transliterates Japanese to Hepburn Romaji and Korean to Revised Romanization.
   - Triggered on-demand via URL query (`?romaji=true`), request body (`target: "romaji"`), or header (`X-Lyrics-Romanize: true`).

6. **Multi-Format Support & Timeline Stripping**:
   - Supports Standard LRC, Enhanced LRC, ESLRC, ASS/SSA, WebVTT, and SRT.
   - Strips karaoke and word-level timestamps (`<mm:ss.xx>`, `{\k...}`) before LLM translation to save 30%~50% tokens and eliminate timestamp desync. Reconstructs line timelines locally.

7. **Song-Switch Concurrent Protection & Decoupled Caching**:
   - When a client disconnects due to rapid song switching, an in-flight LLM translation detaches and finishes in the background, writing to SQLite. Prevents second-time cache misses and wasted tokens.
   - **In-Flight Deduplication**: Merges concurrent identical translation requests.

8. **Ultra-Lightweight (~15MB RAM) Pure Go**:
   - Powered by pure Go (with built-in pure Go SQLite, zero CGO dependencies).
   - Minimal background idle footprint: **~10MB - 15MB RAM**.

---

## 🚀 Quick Start

### Method 1: Download Pre-built Binary

1. Visit [**GitHub Releases**](https://github.com/StarlightJUE/AI-Lyrics-Translate/releases) and download the archive for your OS.
2. Extract and launch the executable (`ai-lyrics-translate.exe` on Windows).
3. On first run, it automatically generates a documented `config.yaml`.
4. Open `http://localhost:5000/` in your browser to manage settings.

---

### Method 2: Run from Source

```bash
# 1. Clone repository
git clone https://github.com/StarlightJUE/AI-Lyrics-Translate.git
cd AI-Lyrics-Translate

# 2. Copy config template
cp config.example.yaml config.yaml

# 3. Run service
go run main.go
```

---

## ⚙️ Configuration (`config.yaml`)

```yaml
# Server Settings
server:
  host: "0.0.0.0"       # Listen address
  port: "5000"          # Business translation port (LibreTranslate compatible)
  admin_port: ""        # Dedicated admin port (leave empty to share port 5000)

# Dual-LLM Configuration
llm:
  priority:
    - "deepseek"
    - "gemini"

  providers:
    deepseek:
      type: "openai"
      base_url: "https://api.deepseek.com"
      api_key: "sk-xxxxxxxx"
      model: "deepseek-chat"
      temperature: 0.2
      timeout_seconds: 60
      max_retries: 2

    gemini:
      type: "gemini"
      base_url: "https://generativelanguage.googleapis.com/v1beta"
      api_key: "AIzaxxxxxxxx"
      model: "gemini-3.5-flash"
      temperature: 0.2
      timeout_seconds: 60
      max_retries: 2

# Translation & Prompts
translation:
  chinese_target_default: "simplified"
  prompt_version: "v1.0"
  custom_meta_prompt: ""
  custom_prompt: ""

# Local SQLite Cache
storage:
  db_path: "lyrics_cache.db"

log_level: "info"
```

---

## 🎧 Client Setup Guide

| Field | Recommended Value | Notes |
| :--- | :--- | :--- |
| **Engine Type** | `LibreTranslate` | Select LibreTranslate protocol |
| **Server Address** | `http://127.0.0.1:5000` | Local relay listening address |
| **API Key** | *(Leave empty)* | Handled internally by local service |
| **Target Language** | `zh` | Or `romaji` for transliteration |

---

## 📄 License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE).

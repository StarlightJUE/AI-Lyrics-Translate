package normalizer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// LineType 标识解析行的时间轴与格式类型
type LineType int

const (
	LineTypeNormal LineType = iota // 普通文本（包括纯文本、SRT/WebVTT 正文）
	LineTypeLRC                   // 标准 LRC、Enhanced LRC、ESLRC（带行首 [mm:ss.xx] 标签）
	LineTypeTTML                  // TTML 段落（带 <p ...> 和 </p>）
	LineTypeASS                   // ASS/SSA 对话行（带 Dialogue: ..., 前缀）
)

var (
	// 匹配行首的标准 LRC 时间戳标签，例如 [01:23.45]、[00:12.345]、[01:23]、[00:01:23.45] 以及多个连续时间戳 [01:23.45][02:34.56]
	reLrcTimestampPrefix = regexp.MustCompile(`^(\s*\[(?:\d{1,2}:)?\d{1,3}:\d{2}(?:\.\d{1,3})?\])+`)

	// 匹配以 <mm:ss.xx> 起始的行首时间戳（部分 Enhanced LRC 变体）
	reAngleTimestampPrefix = regexp.MustCompile(`^(\s*<(?:\d{1,2}:)?\d{1,3}:\d{2}(?:\.\d{1,3})?>)+`)

	// 匹配内嵌逐字/卡拉OK时间戳标签：
	// - <00:12.34> 或 <00:00:12.340> 或 <00:12.34,500> 或 <1200,500> (Enhanced LRC / WebVTT 逐字)
	// - (00:12.34,500) 或 (12000,500) 或 (0,500) (ESLRC / QRC / KRC 逐字变体)
	// - {00:12.34} 或 {12000} (花括号时间戳变体)
	reWordTimestamp = regexp.MustCompile(`(?i)<(?:\d{1,2}:)?\d{1,2}:\d{2}(?:\.\d{1,3})?(?:,\d+)?>|<\d+(?:\.\d+)?(?:,\d+)?>|\((?:\d{1,2}:)?(?:\d{1,2}:)?\d{1,2}(?:\.\d{1,3})?,\d+\)|\(\d+,\d+\)|\{(?:\d{1,2}:)*\d+(?:\.\d+)?\}`)

	// 匹配行内残存的方括号时间戳 [00:12.34]（非行首）
	reInlineBracketTimestamp = regexp.MustCompile(`\[(?:\d{1,2}:)?\d{1,3}:\d{2}(?:\.\d{1,3})?\]`)

	// 匹配 ASS / SSA 特效与控制标签，如 {\k12}、{\kf34}、{\K56}、{\pos(1,2)} 等
	reAssOverrideTag = regexp.MustCompile(`\{[^}]*\}`)

	// 匹配 ASS / SSA 节头，如 [Script Info]、[V4+ Styles]、[Events] 等
	reAssSection = regexp.MustCompile(`^\[.+\]$`)

	// 匹配 ASS / SSA 常见元数据/样式定义属性
	reAssMetadata = regexp.MustCompile(`(?i)^\s*(Title|Original Script|Artist|Audio File|ScriptType|WrapStyle|ScaledBorderAndShadow|YCbCr Matrix|PlayResX|PlayResY|Format|Style|Comment|Video Aspect Ratio|Video Zoom|Video Position|Last Style Storage|Video File|Scroll|Picture):\s*(.*)$`)

	// 匹配 ASS / SSA Dialogue 对话行
	reAssDialogue = regexp.MustCompile(`(?i)^\s*Dialogue:\s*(.*)$`)

	// 匹配 SRT / WebVTT 时间轴箭头行，例如:
	// 00:00:01,000 --> 00:00:04,000 (SRT)
	// 00:00:01.000 --> 00:00:04.000 align:start (WebVTT)
	// 00:01.000 --> 00:04.000 (WebVTT 短格式)
	reSubtitleTimeRange = regexp.MustCompile(`^\s*(?:\d{1,2}:)?\d{2}:\d{2}[,\.]\d{2,3}\s+-->\s+(?:\d{1,2}:)?\d{2}:\d{2}[,\.]\d{2,3}(?:\s+.*)?$`)

	// 匹配 WebVTT 文件头
	reVttHeader = regexp.MustCompile(`(?i)^\s*WEBVTT(\b.*)?$`)

	// 匹配 WebVTT 块标识（NOTE、STYLE、REGION）
	reVttBlockHeader = regexp.MustCompile(`(?i)^\s*(NOTE|STYLE|REGION)(\b.*)?$`)

	// 匹配通用 XML / HTML / TTML 标签，例如 <p begin="...">、<span>、<b>、<font ...>
	reXmlTag = regexp.MustCompile(`<[^>]+>`)

	// 匹配 LRC 元数据行，例如 [ar:歌手], [ti:歌名], [al:专辑], [by:制作人], [offset:0], [length:03:45]
	reLrcMetadata = regexp.MustCompile(`(?i)^\s*\[(ti|title|ar|artist|al|album|by|offset|length|re|ve|encoding|kana|romanized|tool|total)\s*:\s*([^\]]*)\]\s*$`)

	// 匹配 TTML <p ...>...</p> 段落行
	reTtmlPLine = regexp.MustCompile(`(?i)^\s*(<p\b[^>]*>)(.*?)(</p>)\s*$`)

	// 连续空白字符（用于空白合并）
	reMultipleSpaces = regexp.MustCompile(`[ \t\f\r]+`)
)

// ParsedLine 结构化表示解析后的一行
type ParsedLine struct {
	Raw        string   // 原始文本
	PrefixTags string   // 行首提取的时间戳或起始标签（如 [01:23.45]、Dialogue:... 或 <p begin="...">）
	SuffixTags string   // 行尾提取的闭合标签（如 </p>）
	CleanText  string   // 剥离时间戳与标签后的纯净有效文本（无逐字时间戳）
	LineType   LineType // 行格式类型
	IsMetadata bool     // 是否是元数据行或结构行（不送给大模型翻译）
	IsEmpty    bool     // 剥离后是否是空行
}

// ParsedLyrics 歌词/字幕整体解析结构
type ParsedLyrics struct {
	Lines       []ParsedLine
	HasTimeline bool   // 原内容是否包含时间轴标签
	Title       string // 提取的歌曲名
	Artist      string // 提取的歌手/乐队名
}

// FormatContextInfo 格式化歌名与歌手背景信息供大模型理解语境
// 例如：【背景信息】歌曲名：《晴天》，歌手/乐队：周杰伦
func (p *ParsedLyrics) FormatContextInfo() string {
	title := strings.TrimSpace(p.Title)
	artist := strings.TrimSpace(p.Artist)
	if title != "" && artist != "" {
		return fmt.Sprintf("【背景信息】歌曲名：《%s》，歌手/乐队：%s", title, artist)
	} else if title != "" {
		return fmt.Sprintf("【背景信息】歌曲名：《%s》", title)
	} else if artist != "" {
		return fmt.Sprintf("【背景信息】歌手/乐队：%s", artist)
	}
	return ""
}

// cleanLyricsText 剥离行内所有的逐字时间戳与格式控制标签
func cleanLyricsText(text string) string {
	// 1. 剥离 ASS 特效与逐字卡拉OK标签 {\k...}
	clean := reAssOverrideTag.ReplaceAllString(text, "")
	clean = strings.ReplaceAll(clean, `\N`, " ")
	clean = strings.ReplaceAll(clean, `\n`, " ")
	clean = strings.ReplaceAll(clean, `\h`, " ")

	// 2. 剥离逐字/卡拉OK时间戳 (<00:12.34>, (00:12.34,500), {00:12.34} 等)
	clean = reWordTimestamp.ReplaceAllString(clean, "")

	// 3. 剥离行内残留的方括号时间戳 [00:12.34]
	clean = reInlineBracketTimestamp.ReplaceAllString(clean, "")

	// 4. 剥离残存的 XML/HTML 标签 (<span>, <b>, <font> 等)
	clean = reXmlTag.ReplaceAllString(clean, "")

	// 5. 去除首尾空白，合并连续多余空格
	clean = strings.TrimSpace(clean)
	clean = reMultipleSpaces.ReplaceAllString(clean, " ")
	return clean
}

// ParseLyrics 将输入歌词/字幕按行进行结构化拆解，分离时间戳、元数据与纯歌词文字
// 全面支持 Enhanced LRC, ESLRC, ASS/SSA, WebVTT, SRT, TTML 与普通 LRC / 纯文本。
func ParseLyrics(rawText string) *ParsedLyrics {
	// 统一换行符为 \n
	rawText = strings.ReplaceAll(rawText, "\r\n", "\n")
	rawText = strings.ReplaceAll(rawText, "\r", "\n")

	rawLines := strings.Split(rawText, "\n")
	parsedLines := make([]ParsedLine, 0, len(rawLines))
	hasTimeline := false
	var parsedTitle, parsedArtist string

	// 预扫描：标记 SRT / WebVTT 的 Cue 序号或标识行
	// 条件：当前行非空，紧随其后的下一行是时间轴行 (-->)，且上一行是空行或位于文件起始
	isCueId := make(map[int]bool)
	for i := 0; i < len(rawLines)-1; i++ {
		curTrimmed := strings.TrimSpace(rawLines[i])
		nextTrimmed := strings.TrimSpace(rawLines[i+1])
		if curTrimmed != "" && reSubtitleTimeRange.MatchString(nextTrimmed) {
			prevIsEmpty := (i == 0) || strings.TrimSpace(rawLines[i-1]) == ""
			if prevIsEmpty {
				isCueId[i] = true
			}
		}
	}

	inVttBlock := false // 跟踪 WebVTT NOTE / STYLE / REGION 注释块
	inSubtitleCue := false // 跟踪是否处于 SRT / WebVTT 的时间轴正文块中

	for i, line := range rawLines {
		trimmed := strings.TrimSpace(line)

		// WebVTT 注释/样式块状态维护
		if inVttBlock {
			if trimmed == "" {
				inVttBlock = false
				inSubtitleCue = false
				parsedLines = append(parsedLines, ParsedLine{
					Raw:        line,
					IsMetadata: false,
					IsEmpty:    true,
				})
			} else {
				parsedLines = append(parsedLines, ParsedLine{
					Raw:        line,
					IsMetadata: true,
				})
			}
			continue
		}

		// 1. 空行（重置正文块状态）
		if trimmed == "" {
			inSubtitleCue = false
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: false,
				IsEmpty:    true,
			})
			continue
		}

		// 2. WebVTT 文件头
		if reVttHeader.MatchString(trimmed) {
			hasTimeline = true
			inSubtitleCue = false
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 3. WebVTT 块头 (NOTE / STYLE / REGION)
		if reVttBlockHeader.MatchString(trimmed) {
			inVttBlock = true
			inSubtitleCue = false
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 4. SRT / WebVTT Cue 序号/标识行
		if isCueId[i] {
			hasTimeline = true
			inSubtitleCue = false
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 5. SRT / WebVTT 字幕时间轴范围行 (如 00:00:01,000 --> 00:00:04,000)
		if reSubtitleTimeRange.MatchString(trimmed) {
			hasTimeline = true
			inSubtitleCue = true
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 5.1 若处于 SRT / WebVTT 的正文块内，当前行为字幕正文，直接清洗纯文本，不套用行首 LRC 规则
		if inSubtitleCue {
			clean := cleanLyricsText(line)
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				CleanText:  clean,
				LineType:   LineTypeNormal,
				IsMetadata: false,
				IsEmpty:    clean == "",
			})
			continue
		}

		// 6. LRC 元数据标签行 (如 [ti:...], [ar:...])
		if sub := reLrcMetadata.FindStringSubmatch(trimmed); sub != nil {
			tag := strings.ToLower(sub[1])
			val := strings.TrimSpace(sub[2])
			if (tag == "ti" || tag == "title") && parsedTitle == "" {
				parsedTitle = val
			} else if (tag == "ar" || tag == "artist") && parsedArtist == "" {
				parsedArtist = val
			}

			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				PrefixTags: trimmed,
				IsMetadata: true,
			})
			continue
		}

		// 7. ASS / SSA 注释行 (; 或 !)
		if strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "!") {
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 8. ASS / SSA 节头 (如 [Script Info], [Events], [V4+ Styles])
		if reAssSection.MatchString(trimmed) {
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 9. ASS / SSA 元数据或格式定义行 (如 Title: ..., Format: ..., Style: ...)
		if sub := reAssMetadata.FindStringSubmatch(trimmed); sub != nil {
			key := strings.ToLower(sub[1])
			val := strings.TrimSpace(sub[2])
			if key == "title" && parsedTitle == "" {
				parsedTitle = val
			} else if (key == "artist" || key == "original script") && parsedArtist == "" {
				parsedArtist = val
			}

			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 10. 纯 XML 结构行 (如 <?xml...>, <tt...>, <body>, <div>, </div>, </body>, </tt>)
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && reXmlTag.ReplaceAllString(trimmed, "") == "" {
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				IsMetadata: true,
			})
			continue
		}

		// 11. ASS / SSA 对话行 (Dialogue: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text)
		if sub := reAssDialogue.FindStringSubmatch(trimmed); sub != nil {
			hasTimeline = true
			rest := sub[1]
			parts := strings.SplitN(rest, ",", 10)
			var prefix, textPart string
			if len(parts) == 10 {
				commaCount := 0
				splitIdx := -1
				for cIdx, r := range rest {
					if r == ',' {
						commaCount++
						if commaCount == 9 {
							splitIdx = cIdx
							break
						}
					}
				}
				if splitIdx != -1 {
					dialoguePrefixLen := strings.Index(line, rest)
					pos := dialoguePrefixLen + splitIdx + 1
					prefix = line[:pos]
					textPart = line[pos:]
				} else {
					prefix = line
					textPart = ""
				}
			} else {
				prefix = line
				textPart = ""
			}

			clean := cleanLyricsText(textPart)
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				PrefixTags: prefix,
				CleanText:  clean,
				LineType:   LineTypeASS,
				IsMetadata: false,
				IsEmpty:    clean == "",
			})
			continue
		}

		// 12. TTML <p ...>...</p> 段落行
		if sub := reTtmlPLine.FindStringSubmatch(trimmed); sub != nil {
			hasTimeline = true
			clean := cleanLyricsText(sub[2])
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				PrefixTags: sub[1],
				SuffixTags: sub[3],
				CleanText:  clean,
				LineType:   LineTypeTTML,
				IsMetadata: false,
				IsEmpty:    clean == "",
			})
			continue
		}

		// 13. 标准 LRC / Enhanced LRC / ESLRC 行首时间戳 [mm:ss.xx]
		if loc := reLrcTimestampPrefix.FindStringIndex(line); loc != nil {
			hasTimeline = true
			prefix := line[loc[0]:loc[1]]
			textPart := line[loc[1]:]
			clean := cleanLyricsText(textPart)
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				PrefixTags: prefix,
				CleanText:  clean,
				LineType:   LineTypeLRC,
				IsMetadata: false,
				IsEmpty:    clean == "",
			})
			continue
		}

		// 14. Enhanced LRC 行首时间戳以 <mm:ss.xx> 起始的变体
		if loc := reAngleTimestampPrefix.FindStringIndex(line); loc != nil {
			hasTimeline = true
			rawPrefix := line[loc[0]:loc[1]]
			lrcPrefix := strings.ReplaceAll(strings.ReplaceAll(rawPrefix, "<", "["), ">", "]")
			textPart := line[loc[1]:]
			clean := cleanLyricsText(textPart)
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				PrefixTags: lrcPrefix,
				CleanText:  clean,
				LineType:   LineTypeLRC,
				IsMetadata: false,
				IsEmpty:    clean == "",
			})
			continue
		}

		// 15. 普通文本行（包括 SRT / WebVTT 的歌词文本行、无时间轴的纯文本歌词）
		clean := cleanLyricsText(line)
		parsedLines = append(parsedLines, ParsedLine{
			Raw:        line,
			CleanText:  clean,
			LineType:   LineTypeNormal,
			IsMetadata: false,
			IsEmpty:    clean == "",
		})
	}

	return &ParsedLyrics{
		Lines:       parsedLines,
		HasTimeline: hasTimeline,
		Title:       parsedTitle,
		Artist:      parsedArtist,
	}
}

// ExtractCleanLinesForLLM 提取出需要喂给大模型翻译的纯文本歌词列表
// 过滤掉元数据行与空行，仅保留有实际歌词内容的行
func (p *ParsedLyrics) ExtractCleanLinesForLLM() []string {
	var cleanLines []string
	for _, l := range p.Lines {
		if !l.IsMetadata && !l.IsEmpty {
			cleanLines = append(cleanLines, l.CleanText)
		}
	}
	return cleanLines
}

// GenerateCacheKey 根据歌词内容与目标语言生成标准化内容感知哈希 (SHA-256)
// 规范化规则：
// 1. 剥离所有 LRC/Enhanced LRC/ASS/WebVTT/SRT/TTML 时间戳和元数据标签
// 2. 去除首尾空格、合并多余连续空白符、统一换行符 \n
// 3. 英文统一转为小写
// 4. 计算 SHA256(规范化文本 + ":" + 目标语言)
func GenerateCacheKey(rawText, targetLang string) (string, string) {
	// 解析并提取所有有效歌词行
	parsed := ParseLyrics(rawText)
	cleanLines := parsed.ExtractCleanLinesForLLM()

	// 转换为小写并使用换行符连接
	var normalizedLines []string
	for _, line := range cleanLines {
		norm := strings.ToLower(line)
		norm = strings.TrimSpace(norm)
		norm = reMultipleSpaces.ReplaceAllString(norm, " ")
		if norm != "" {
			normalizedLines = append(normalizedLines, norm)
		}
	}

	normalizedText := strings.Join(normalizedLines, "\n")

	// 计算 SHA-256
	hasher := sha256.New()
	hasher.Write([]byte(normalizedText))
	hasher.Write([]byte(":" + strings.ToLower(strings.TrimSpace(targetLang))))
	hashKey := hex.EncodeToString(hasher.Sum(nil))

	return hashKey, normalizedText
}

// RebuildLyrics 将大模型返回的翻译行与原始结构重组
// 如果原歌词包含时间戳，精确回填行级时间轴标签；
// 对于逐字时间轴格式（Enhanced LRC、ESLRC、ASS/SSA、WebVTT 逐字），拼回时自动去除逐字时间轴，仅保留逐行时间轴。
func (p *ParsedLyrics) RebuildLyrics(translatedCleanLines []string) string {
	var result []string
	transIdx := 0
	transCount := len(translatedCleanLines)

	for _, line := range p.Lines {
		if line.IsMetadata {
			// 保留原始元数据行（如 [ti:...], WebVTT/SRT 时间轴行, ASS Style/Header 等）
			result = append(result, line.Raw)
			continue
		}

		if line.IsEmpty {
			// 原来是空行或纯间奏时间戳行（如 [01:23.45] 无歌词）
			if line.PrefixTags != "" {
				result = append(result, line.PrefixTags)
			} else {
				result = append(result, "")
			}
			continue
		}

		// 取出对应翻译行
		var translated string
		if transIdx < transCount {
			translated = translatedCleanLines[transIdx]
			transIdx++
		} else {
			// 兜底：若翻译行数不足，使用原文本以防内容丢失
			translated = line.CleanText
		}

		switch line.LineType {
		case LineTypeASS:
			// ASS 风格：PrefixTags 已保留到第 9 个逗号，直接拼接翻译文本（去除所有逐字卡拉OK特效）
			result = append(result, line.PrefixTags+translated)
		case LineTypeTTML:
			// TTML 标签风格：<p ...>翻译文本</p>
			result = append(result, line.PrefixTags+translated+line.SuffixTags)
		case LineTypeLRC:
			// LRC 标签风格：[00:01.00] 翻译文本（去除了所有行内的逐字时间戳）
			if strings.HasSuffix(line.PrefixTags, " ") {
				result = append(result, line.PrefixTags+translated)
			} else {
				result = append(result, line.PrefixTags+" "+translated)
			}
		default:
			// 普通文本（包括 SRT / WebVTT 正文行、纯文本歌词）
			result = append(result, translated)
		}
	}

	return strings.Join(result, "\n")
}

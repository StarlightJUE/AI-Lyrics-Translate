package normalizer

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	// 匹配行首的标准 LRC 时间戳标签，例如 [01:23.45]、[00:12.345]、[01:23] 以及多个连续时间戳 [01:23.45][02:34.56]
	reLrcTimestampPrefix = regexp.MustCompile(`^(\s*\[\d{1,3}:\d{2}(?:\.\d{1,3})?\])+`)

	// 匹配内联/字级时间戳标签，例如 <00:12.34> 或 <01:23>
	reInlineTimestamp = regexp.MustCompile(`<\d{1,3}:\d{2}(?:\.\d{1,3})?>`)

	// 匹配通用 XML / HTML / TTML 标签，例如 <p begin="..."> 或 </span>
	reXmlTag = regexp.MustCompile(`<[^>]+>`)

	// 匹配 LRC 元数据行，例如 [ar:歌手], [ti:歌名], [al:专辑], [by:制作人], [offset:0], [length:03:45]
	reLrcMetadata = regexp.MustCompile(`(?i)^\s*\[(ti|ar|al|by|offset|length|re|ve|encoding|kana|romanized|tool):[^\]]*\]\s*$`)

	// 匹配 TTML <p ...>...</p> 段落行
	reTtmlPLine = regexp.MustCompile(`(?i)^\s*(<p\b[^>]*>)(.*?)(</p>)\s*$`)

	// 连续空白字符（用于空白合并）
	reMultipleSpaces = regexp.MustCompile(`[ \t\f\r]+`)
)

// ParsedLine 结构化表示解析后的一行
type ParsedLine struct {
	Raw        string // 原始文本
	PrefixTags string // 行首提取的时间戳或起始标签（如 [01:23.45] 或 <p begin="...">）
	SuffixTags string // 行尾提取的闭合标签（如 </p>）
	CleanText  string // 剥离时间戳与标签后的有效文本
	IsMetadata bool   // 是否是 LRC 元数据行（如 [ti:...]）或纯 XML 结构行（如 <tt>, <body>）
	IsEmpty    bool   // 剥离后是否是空行
}

// ParsedLyrics 歌词整体解析结构
type ParsedLyrics struct {
	Lines       []ParsedLine
	HasTimeline bool // 原歌词是否包含时间轴标签
}

// ParseLyrics 将输入歌词按行进行结构化拆解，分离时间戳、元数据与纯歌词文字
func ParseLyrics(rawText string) *ParsedLyrics {
	// 统一换行符为 \n
	rawText = strings.ReplaceAll(rawText, "\r\n", "\n")
	rawText = strings.ReplaceAll(rawText, "\r", "\n")

	rawLines := strings.Split(rawText, "\n")
	parsedLines := make([]ParsedLine, 0, len(rawLines))
	hasTimeline := false

	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)

		// 1. 判断是否为 LRC 元数据标签行
		if reLrcMetadata.MatchString(trimmed) {
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				PrefixTags: trimmed,
				CleanText:  "",
				IsMetadata: true,
				IsEmpty:    false,
			})
			continue
		}

		// 2. 判断是否为纯 XML 结构行（如 <?xml...>, <tt...>, <body>, <div>, </div>, </body>, </tt> 无文本内容）
		if strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && reXmlTag.ReplaceAllString(trimmed, "") == "" {
			parsedLines = append(parsedLines, ParsedLine{
				Raw:        line,
				CleanText:  "",
				IsMetadata: true,
				IsEmpty:    false,
			})
			continue
		}

		var prefix, suffix string
		textPart := line

		// 3. 提取 TTML <p ...>...</p> 标签与内部歌词
		if sub := reTtmlPLine.FindStringSubmatch(trimmed); sub != nil {
			hasTimeline = true
			prefix = sub[1]
			textPart = sub[2]
			suffix = sub[3]
		} else if loc := reLrcTimestampPrefix.FindStringIndex(line); loc != nil {
			// 4. 提取标准 LRC 行首时间戳 [mm:ss.xx]
			hasTimeline = true
			prefix = line[loc[0]:loc[1]]
			textPart = line[loc[1]:]
		}

		// 5. 剥离内嵌时间戳与残留 XML/HTML 标签
		clean := reInlineTimestamp.ReplaceAllString(textPart, "")
		clean = reXmlTag.ReplaceAllString(clean, "")

		// 去除首尾空白，合并连续多余空格
		clean = strings.TrimSpace(clean)
		clean = reMultipleSpaces.ReplaceAllString(clean, " ")

		isEmpty := (clean == "")

		parsedLines = append(parsedLines, ParsedLine{
			Raw:        line,
			PrefixTags: prefix,
			SuffixTags: suffix,
			CleanText:  clean,
			IsMetadata: false,
			IsEmpty:    isEmpty,
		})
	}

	return &ParsedLyrics{
		Lines:       parsedLines,
		HasTimeline: hasTimeline,
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
// 1. 剥离所有 LRC/TTML 时间戳和元数据标签
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
// 如果原歌词包含时间戳，则精确回填时间轴标签，100% 保持时间对齐与元数据
func (p *ParsedLyrics) RebuildLyrics(translatedCleanLines []string) string {
	var result []string
	transIdx := 0
	transCount := len(translatedCleanLines)

	for _, line := range p.Lines {
		if line.IsMetadata {
			// 保留原始元数据行（如 [ti:...]）
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

		if line.PrefixTags != "" && line.SuffixTags != "" {
			// TTML 标签风格：<p ...>翻译文本</p>
			result = append(result, line.PrefixTags+translated+line.SuffixTags)
		} else if line.PrefixTags != "" {
			// LRC 时间戳标签风格：[00:01.00] 翻译文本
			result = append(result, line.PrefixTags+" "+translated)
		} else {
			result = append(result, translated)
		}
	}

	return strings.Join(result, "\n")
}

package llm

import (
	"fmt"
	"strings"
)

// MetaPromptFramework 硬编码元提示词系统框架（保证行数对应、纯净格式、绝不输出背景信息）
const MetaPromptFramework = `你是一位专业音乐歌词翻译大师。请严格按照给定的【用户提示词】要求，将输入的歌词精准翻译为目标语言「{target}」。

【硬性约束（最高优先级，绝不可违背）】：
1. 严格行数对应：输入多少行有效歌词，就必须输出多少行译文，每行必须严格 1 对 1 对应，绝对不可合并行、拆分行、增加行或遗漏行。
2. 纯净输出格式：仅输出翻译后的歌词文本，绝不包含任何 Markdown 代码块（如 ` + "```" + `）、行号、拼音注音、前缀或注释。
3. 严禁输出背景信息：输入中可能附带歌曲的【背景信息】（如歌曲名、歌手/乐队等），该信息仅供你深入理解曲风、语境与情感色彩，绝对禁止在输出结果中输出任何背景信息、曲名或相关解释文字！
4. 源语言自主识别：自行辨别歌词实际语种（不要盲从可能误判的外部语言设定，准确识别拉丁文、法语、德语、西班牙语、日语、韩语等）。
5. 原文语言保护：若输入歌词本身已是目标语言，直接原样保留（若为中文且需简繁转换，则仅做字形转换，严禁篡改原词曲意）。`

// DefaultUserPromptZhHans 简体中文文学级词作调优提示词
const DefaultUserPromptZhHans = `【核心审美追求（坚决杜绝平庸直白）】：
1. 意境传神胜于字面直译：深入领会整首歌词的情感张力与哲思，重在重塑诗意美感与宏大画面，严禁使用干瘪的大白话、说明文或机械直译腔。
2. 词句雅致富有灵气：遣词典雅考究，融入中文歌词独有的含蓄与诗意（例如：“荒漠亦曾浩瀚成海”远优于“一些沙漠曾经是海洋”；“愿所有美好皆被温柔以待”远优于“希望所有美丽都被祝福”）。
3. 音律与节奏感：译文需读来琅琅上口、语感凝练，符合中文音乐听觉美学。`

// DefaultUserPromptZhHant 繁体中文文学级词作调优提示词
const DefaultUserPromptZhHant = `【核心審美追求（堅決杜絕平庸直白）】：
1. 意境傳神勝於字面直譯：深入領會整首歌曲的情感張力與哲思，重在重塑詩意美感與宏大畫面，嚴禁使用乾癟的大白話、說明文或機械直譯腔。
2. 詞句雅致富有靈氣：遣詞典雅考究，融入中文歌詞獨有的含蓄與詩意（例如：「荒漠亦曾浩瀚成海」遠優於「一些沙漠曾經是海洋」；「願所有美好皆被溫柔以待」遠優於「希望所有美麗都被祝福」）。
3. 音律與節奏感：譯文需讀來琅琅上口、語感凝練，符合中文音樂聽覺美學。`

// DefaultUserPromptGeneral 通用语言文学调优提示词
const DefaultUserPromptGeneral = `【Core Artistic Standard】：
1. Literary Quality & Nuance: Keep the poetic elegance, emotional depth, and evocative imagery of the original song.
2. Musical Cadence: Choose words that sound lyrical and rhythmic when sung.
3. Natural Phrasing: Strictly avoid mechanical, word-for-word translation; recreate the poetic meaning gracefully.`

// RomajiSystemPrompt 日韩语转罗马音专用提示词
const RomajiSystemPrompt = `你是一位精通日语罗马字（Hepburn Romaji）与韩语罗马音（Revised Romanization of Korean）的专业语音转写专家。
你的任务是将输入的日文或韩文歌词，逐行转写为规范易读的标准罗马音（拉丁字母注音）。

【转写规范与要求】：
1. 日语歌词：
   - 深入结合歌词语境与音乐演唱习惯，精准判断汉字在特定歌词中的实际读音（准确区分音读/训读及特殊读法），转写为赫本式罗马字（Hepburn Romaji）；
   - 全部转写为标准小写拉丁字母，词组间以自然空格分隔，符合歌词阅读与跟唱习惯。
2. 韩语歌词：
   - 严格遵循韩国文化观光部《国语罗马字表记法》（Revised Romanization of Korean）；
   - 考虑实际歌唱连音、鼻音化、流音化等音变，还原标准演唱发音。
3. 英文与其它语言行：
   - 若某行歌词原本已是英文、外语拉丁字母或伴唱虚词，完整原样保留。

【硬性约束（最高优先级）】：
1. 严格行数对应：输入多少行有效歌词，就必须输出多少行罗马音，每行严格 1 对 1 对应，绝不可合并行、拆分行或遗漏行。
2. 纯净输出格式：仅输出转写后的罗马音文本，绝不包含任何 Markdown 代码块（如 ` + "```" + `）、行号、假名原文或解释。
3. 严禁输出背景信息：输入中可能附带【背景信息】（如歌曲名、歌手），仅供理解语境，绝不可在输出中输出任何背景信息。`

// BuildSystemPrompt 拼接元提示词框架（默认或自定义）与用户提示词
func BuildSystemPrompt(targetLang, customUserPrompt, customMetaPrompt string, isRomanize bool) string {
	if isRomanize {
		return RomajiSystemPrompt
	}

	targetName := formatTargetLangName(targetLang)
	var meta string
	if strings.TrimSpace(customMetaPrompt) != "" {
		meta = strings.ReplaceAll(customMetaPrompt, "{target}", targetName)
	} else {
		meta = strings.ReplaceAll(MetaPromptFramework, "{target}", targetName)
	}

	var userPrompt string
	if strings.TrimSpace(customUserPrompt) != "" {
		userPrompt = strings.ReplaceAll(customUserPrompt, "{target}", targetName)
	} else {
		userPrompt = GetDefaultUserPrompt(targetLang)
	}

	return meta + "\n\n【用户提示词】\n" + userPrompt
}

// BuildUserContent 构造送给大模型的消息文本，包含行数强调与背景信息
func BuildUserContent(cleanLines []string, contextInfo string, attempt int) string {
	var sb strings.Builder
	if strings.TrimSpace(contextInfo) != "" {
		sb.WriteString(strings.TrimSpace(contextInfo))
		sb.WriteString("\n（注意：上述背景信息仅供理解语境与情绪参考，严禁在输出中包含背景信息，必须严格按行输出）\n\n")
	}

	if attempt > 1 {
		sb.WriteString(fmt.Sprintf("【注意：必须输出严格 %d 行，每一行一一对应，不要增加或减少任何一行】\n\n", len(cleanLines)))
	}

	sb.WriteString(strings.Join(cleanLines, "\n"))
	return sb.String()
}

// GetDefaultUserPrompt 导出默认用户文学调优提示词
func GetDefaultUserPrompt(targetLang string) string {
	target := strings.ToLower(strings.TrimSpace(targetLang))
	if target == "zh-hant" || target == "zt" || target == "zh-tw" || target == "zh-hk" || target == "traditional" {
		return DefaultUserPromptZhHant
	}
	if target == "zh-hans" || target == "zh-cn" || target == "chs" || target == "zh" || target == "" || target == "simplified" {
		return DefaultUserPromptZhHans
	}
	return DefaultUserPromptGeneral
}

func formatTargetLangName(targetLang string) string {
	target := strings.ToLower(strings.TrimSpace(targetLang))
	switch target {
	case "zh-hans", "zh-cn", "chs", "zh", "simplified":
		return "简体中文"
	case "zh-hant", "zt", "zh-tw", "zh-hk", "traditional":
		return "繁體中文"
	case "en":
		return "English"
	case "ja":
		return "日本語"
	case "ko":
		return "한국어"
	case "fr":
		return "Français"
	case "de":
		return "Deutsch"
	case "es":
		return "Español"
	default:
		if targetLang == "" {
			return "简体中文"
		}
		return targetLang
	}
}

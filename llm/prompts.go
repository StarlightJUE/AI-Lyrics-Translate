package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MetaPromptFramework 硬编码元提示词系统框架（保证行号严格对齐、同语种返回空行、绝不乱吞正文）
const MetaPromptFramework = `你是一位专业音乐歌词翻译大师。请严格按照给定的【用户提示词】要求，将输入的带编号 JSON 歌词逐行精准翻译为目标语言「{target}」。

【硬性约束（最高优先级，绝不可违背）】：
1. 严格行数对应与 JSON 键名锁定：输入是一个以行号字符串（"0", "1", ...）为键的 JSON 对象。你必须输出一个合法的 JSON 对象，包含与输入完全一致的所有行号键名（从 "0" 到最大行号，共 N 个键），绝对不可合并、拆分、增加或遗漏任何编号键（即便某行内容是歌曲名、作词、作曲、编曲、演唱等制作人员信息，只要它在输入 JSON 中有编号，就必须保留该编号并输出对应键！）。
2. 纯净 JSON 输出格式：仅输出一个标准 JSON 对象，绝不包含任何 Markdown 代码块（如 ` + "```" + `）、行号前缀、注释或前言后记。
3. 严禁输出背景信息：输入 JSON 之外附带的【背景信息】段落（如歌曲名、歌手/乐队等）仅供你理解曲风与语境，绝对禁止把那段【背景信息】作为额外键名混入输出 JSON 中！
4. 源语言自主识别：自行辨别每行歌词实际语种（不要盲从外部可能误判的语言设定，准确区分中文、日语、韩语、英语、拉丁文、法语、德语、西班牙语等；结合整首歌语境准确分辨纯汉字行是中文还是日语）。
5. 同语种直接返回空行：如果某行歌词本身已经是目标语言「{target}」（例如目标为简体中文且该行本身就是中文歌词或中文制作人员行，或目标为英文且该行本身就是英文），或者是无需翻译的纯符号/表情/音乐标识，不要原样照抄原文，必须直接将对应键的值设为空字符串 ""！（若仅需中文简繁字形转换且字形确有差异时才转换，转换后若与原文完全相同同样设为 ""）。`

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
const RomajiSystemPrompt = `你是一位精通日语罗马字（Hepburn Romaji）、韩语罗马音（Revised Romanization of Korean）与汉语拼音（Pinyin）的专业语音转写专家。
你的任务是将输入的带编号 JSON 歌词，逐行转写为规范易读的标准罗马音或拼音（拉丁字母注音）。

【转写规范与要求】：
1. 日语歌词：
   - 深入结合歌词语境与音乐演唱习惯，精准判断汉字在特定歌词中的实际读音（准确区分音读/训读及特殊读法），转写为赫本式罗马字（Hepburn Romaji）；
   - 全部转写为标准小写拉丁字母，词组间以自然空格分隔，符合歌词阅读与跟唱习惯。
2. 韩语歌词：
   - 严格遵循韩国文化观光部《国语罗马字表记法》（Revised Romanization of Korean）；
   - 考虑实际歌唱连音、鼻音化、流音化等音变，还原标准演唱发音。
3. 中文歌词：
   - 若歌词包含中文汉字，转写为规范易读的小写汉语拼音（Pinyin），词组间以自然空格分隔，便于跟唱跟读。
4. 英文与其它语言行：
   - 若某行歌词原本已是英文、外语拉丁字母或伴唱虚词，完整原样保留。

【硬性约束（最高优先级）】：
1. 严格行数对应与 JSON 键名锁定：输入是一个以行号字符串（"0", "1", ...）为键的 JSON 对象。你必须输出一个合法的 JSON 对象，包含与输入完全一致的所有行号键名（从 "0" 到最大行号），绝对不可合并、拆分或遗漏任何编号键。
2. 纯净 JSON 输出格式：仅输出一个标准 JSON 对象，绝不包含任何 Markdown 代码块（如 ` + "```" + `）、解释文字或前言后记。
3. 严禁输出背景信息：输入 JSON 之外附带的【背景信息】仅供理解语境，绝不可把背景信息额外输出进 JSON 中！`

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

// BuildUserContent 构造送给大模型的消息文本，包含行数强调、JSON 索引结构与背景信息
func BuildUserContent(cleanLines []string, contextInfo string, attempt int) string {
	return BuildUserContentWithMissing(cleanLines, contextInfo, attempt, nil)
}

// BuildUserContentWithMissing 构造带重试缺失键提醒的 JSON 索引输入消息
func BuildUserContentWithMissing(cleanLines []string, contextInfo string, attempt int, missingIndices []int) string {
	var sb strings.Builder
	if strings.TrimSpace(contextInfo) != "" {
		sb.WriteString(strings.TrimSpace(contextInfo))
		sb.WriteString("\n（注意：上述背景信息仅供理解语境与情绪参考，严禁在输出中包含此背景信息；但下方 JSON 输入中的每一条编号都必须保留在输出 JSON 中！）\n\n")
	}

	if attempt > 1 {
		if len(missingIndices) > 0 {
			var missingStr []string
			for _, idx := range missingIndices {
				missingStr = append(missingStr, fmt.Sprintf("%q", fmt.Sprintf("%d", idx)))
			}
			sb.WriteString(fmt.Sprintf("【重要重试提醒：上次输出缺少了编号 %s！必须输出包含全部 %d 个键（\"0\" 到 \"%d\"）的完整 JSON 对象，同语种行请设为空字符串 \"\"，绝不可删除或遗漏任何编号】\n\n",
				strings.Join(missingStr, ", "), len(cleanLines), len(cleanLines)-1))
		} else {
			sb.WriteString(fmt.Sprintf("【重要重试提醒：必须输出标准 JSON 对象，严格包含 \"0\" 到 \"%d\" 共 %d 个键，不可增加或遗漏任何键，同语种行请设为空字符串 \"\"】\n\n",
				len(cleanLines)-1, len(cleanLines)))
		}
	}

	sb.WriteString(buildIndexedJSONInput(cleanLines))
	return sb.String()
}

func buildIndexedJSONInput(lines []string) string {
	var sb strings.Builder
	sb.WriteString("{\n")
	for i, line := range lines {
		keyBytes, _ := json.Marshal(fmt.Sprintf("%d", i))
		valBytes, _ := json.Marshal(line)
		sb.WriteString("  ")
		sb.Write(keyBytes)
		sb.WriteString(": ")
		sb.Write(valBytes)
		if i < len(lines)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}")
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

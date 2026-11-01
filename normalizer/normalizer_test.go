package normalizer

import (
	"strings"
	"testing"
)

func TestParseAndRebuildLRC(t *testing.T) {
	inputLrc := `[ti:Yesterday Once More]
[ar:Carpenters]
[00:01.00]When I was young
[00:04.50]I'd listen to the radio
[00:08.00]
[00:10.20]<00:10.25>Waiting for my favorite songs`

	parsed := ParseLyrics(inputLrc)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true, got false")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	expectedCount := 3 // When I was young, I'd listen to the radio, Waiting for my favorite songs
	if len(cleanLines) != expectedCount {
		t.Fatalf("expected %d clean lines, got %d: %#v", expectedCount, len(cleanLines), cleanLines)
	}

	if cleanLines[2] != "Waiting for my favorite songs" {
		t.Errorf("inline tag not stripped: %s", cleanLines[2])
	}

	// 模拟 LLM 翻译返回
	translatedLines := []string{
		"当我年少时",
		"我常常守着收音机",
		"等待着我最爱的旋律",
	}

	rebuilt := parsed.RebuildLyrics(translatedLines)

	// 校验元数据被原样保留
	if !strings.Contains(rebuilt, "[ti:Yesterday Once More]") {
		t.Errorf("metadata missing in rebuilt: %s", rebuilt)
	}
	// 校验时间戳被准确拼接
	if !strings.Contains(rebuilt, "[00:01.00] 当我年少时") {
		t.Errorf("timestamp prefix missing in rebuilt: %s", rebuilt)
	}
	if !strings.Contains(rebuilt, "[00:08.00]") {
		t.Errorf("empty timestamp line missing in rebuilt: %s", rebuilt)
	}
}

func TestGenerateCacheKey(t *testing.T) {
	lrc1 := `[00:01.00] Hello WORLD `
	lrc2 := `[00:01.50]  hello   world`

	key1, norm1 := GenerateCacheKey(lrc1, "zh")
	key2, norm2 := GenerateCacheKey(lrc2, "zh")

	if key1 != key2 {
		t.Errorf("expected identical keys for normalized content, got %s and %s", key1, key2)
	}
	if norm1 != norm2 {
		t.Errorf("expected identical normalized text, got %s and %s", norm1, norm2)
	}
	if norm1 != "hello world" {
		t.Errorf("expected 'hello world', got %s", norm1)
	}
}

func TestPlainTextNoTimeline(t *testing.T) {
	plainText := "Hello world\nHow are you"
	parsed := ParseLyrics(plainText)
	if parsed.HasTimeline {
		t.Errorf("expected HasTimeline=false")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d", len(cleanLines))
	}

	rebuilt := parsed.RebuildLyrics([]string{"你好世界", "你怎么样"})
	expected := "你好世界\n你怎么样"
	if rebuilt != expected {
		t.Errorf("expected %q, got %q", expected, rebuilt)
	}
}

func TestParseAndRebuildTTML(t *testing.T) {
	ttmlInput := `<?xml version="1.0" encoding="utf-8"?>
<tt xmlns="http://www.w3.org/ns/ttml">
  <body>
    <div>
      <p begin="00:00:01.000" end="00:00:04.000">When I was young</p>
      <p begin="00:00:04.500" end="00:00:08.000">I listened to the radio</p>
    </div>
  </body>
</tt>`

	parsed := ParseLyrics(ttmlInput)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true for TTML")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d: %#v", len(cleanLines), cleanLines)
	}
	if cleanLines[0] != "When I was young" || cleanLines[1] != "I listened to the radio" {
		t.Errorf("unexpected clean lines: %#v", cleanLines)
	}

	rebuilt := parsed.RebuildLyrics([]string{"当我年少时", "我常常倾听收音机"})

	// 验证 XML 外部结构被原样保留
	if !strings.Contains(rebuilt, `<tt xmlns="http://www.w3.org/ns/ttml">`) {
		t.Errorf("missing <tt> root: %s", rebuilt)
	}
	if !strings.Contains(rebuilt, `<body>`) || !strings.Contains(rebuilt, `<div>`) {
		t.Errorf("missing wrapper tags: %s", rebuilt)
	}

	// 验证 <p begin="..." end="..."> 标签与内部翻译准确回填
	if !strings.Contains(rebuilt, `<p begin="00:00:01.000" end="00:00:04.000">当我年少时</p>`) {
		t.Errorf("expected rebuilt TTML line 1 missing, got: %s", rebuilt)
	}
	if !strings.Contains(rebuilt, `<p begin="00:00:04.500" end="00:00:08.000">我常常倾听收音机</p>`) {
		t.Errorf("expected rebuilt TTML line 2 missing, got: %s", rebuilt)
	}
}

// TestParseAndRebuildEnhancedLRC 验证 Enhanced LRC (带 <mm:ss.xx> 逐字时间戳) 能够正确提取纯文本，重组时剥离逐字时间戳并保留行级时间轴
func TestParseAndRebuildEnhancedLRC(t *testing.T) {
	enhancedLrc := `[00:01.00]<00:01.00>When <00:01.50>I <00:02.00>was <00:02.50>young
[00:04.50]<00:04.50>I <00:05.00>listened <00:05.50>to <00:06.00>the <00:06.50>radio`

	parsed := ParseLyrics(enhancedLrc)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true for Enhanced LRC")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d", len(cleanLines))
	}
	if cleanLines[0] != "When I was young" {
		t.Errorf("expected 'When I was young', got %q", cleanLines[0])
	}
	if cleanLines[1] != "I listened to the radio" {
		t.Errorf("expected 'I listened to the radio', got %q", cleanLines[1])
	}

	rebuilt := parsed.RebuildLyrics([]string{"当我年少时", "我常常倾听收音机"})

	// 校验：保留逐行时间轴，且绝对不包含任何逐字时间戳标签
	if !strings.Contains(rebuilt, "[00:01.00] 当我年少时") {
		t.Errorf("expected rebuilt line 1 missing, got:\n%s", rebuilt)
	}
	if !strings.Contains(rebuilt, "[00:04.50] 我常常倾听收音机") {
		t.Errorf("expected rebuilt line 2 missing, got:\n%s", rebuilt)
	}
	if strings.Contains(rebuilt, "<00:") {
		t.Errorf("word-by-word timestamps must be stripped from rebuilt result, got:\n%s", rebuilt)
	}
}

// TestParseAndRebuildESLRC 验证 ESLRC / 卡拉OK格式 (带 (mm:ss.xx,duration) 或 (offset,duration))
func TestParseAndRebuildESLRC(t *testing.T) {
	eslrcInput := `[00:01.00]When(00:01.00,500) I(00:01.50,500) was(00:02.00,500) young(00:02.50,500)
[00:04.50]I(4500,500) listened(5000,500) to(5500,500) the(6000,500) radio(6500,500)`

	parsed := ParseLyrics(eslrcInput)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true for ESLRC")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d", len(cleanLines))
	}
	if cleanLines[0] != "When I was young" || cleanLines[1] != "I listened to the radio" {
		t.Errorf("unexpected clean lines: %#v", cleanLines)
	}

	rebuilt := parsed.RebuildLyrics([]string{"当我年少时", "我常常倾听收音机"})
	if !strings.Contains(rebuilt, "[00:01.00] 当我年少时") || !strings.Contains(rebuilt, "[00:04.50] 我常常倾听收音机") {
		t.Errorf("expected rebuilt lines missing, got:\n%s", rebuilt)
	}
	if strings.Contains(rebuilt, "(") || strings.Contains(rebuilt, ")") {
		t.Errorf("word timestamps should be stripped, got:\n%s", rebuilt)
	}
}

// TestParseAndRebuildASS 验证 ASS / SSA (Advanced SubStation Alpha) 格式解析与卡拉OK特效 {\k...} 去除
func TestParseAndRebuildASS(t *testing.T) {
	assInput := `[Script Info]
Title: Test ASS
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,{\k100}When {\k50}I {\k50}was {\k100}young
Dialogue: 0,0:00:04.50,0:00:08.00,Default,,0,0,0,,{\kf50}I {\kf50}listened {\kf50}to {\kf50}the {\kf50}radio
Comment: 0,0:00:00.00,0:00:00.00,Default,,0,0,0,,Internal note`

	parsed := ParseLyrics(assInput)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true for ASS")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d: %#v", len(cleanLines), cleanLines)
	}
	if cleanLines[0] != "When I was young" || cleanLines[1] != "I listened to the radio" {
		t.Errorf("unexpected clean lines: %#v", cleanLines)
	}

	rebuilt := parsed.RebuildLyrics([]string{"当我年少时", "我常常倾听收音机"})

	// 验证头信息原样保留
	if !strings.Contains(rebuilt, "[Script Info]") || !strings.Contains(rebuilt, "ScriptType: v4.00+") {
		t.Errorf("ASS headers missing in rebuilt:\n%s", rebuilt)
	}
	if !strings.Contains(rebuilt, "[Events]") || !strings.Contains(rebuilt, "Format: Layer, Start, End, Style") {
		t.Errorf("ASS Events table missing in rebuilt:\n%s", rebuilt)
	}
	// 验证 Comment 原样保留
	if !strings.Contains(rebuilt, "Comment: 0,0:00:00.00,0:00:00.00,Default,,0,0,0,,Internal note") {
		t.Errorf("ASS Comment missing in rebuilt:\n%s", rebuilt)
	}
	// 验证 Dialogue 行级时间轴完整保留，且已剥离 {\k...} 特效
	expectedDial1 := "Dialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,当我年少时"
	expectedDial2 := "Dialogue: 0,0:00:04.50,0:00:08.00,Default,,0,0,0,,我常常倾听收音机"
	if !strings.Contains(rebuilt, expectedDial1) {
		t.Errorf("expected %q, got:\n%s", expectedDial1, rebuilt)
	}
	if !strings.Contains(rebuilt, expectedDial2) {
		t.Errorf("expected %q, got:\n%s", expectedDial2, rebuilt)
	}
	if strings.Contains(rebuilt, `{\k`) || strings.Contains(rebuilt, `{\kf`) {
		t.Errorf("karaoke tags must be removed from ASS output, got:\n%s", rebuilt)
	}
}

// TestParseAndRebuildWebVTT 验证 WebVTT 格式（带头部、Cue 标识、时间轴范围及行内逐字时间戳）
func TestParseAndRebuildWebVTT(t *testing.T) {
	vttInput := `WEBVTT - Demo subtitles

NOTE
This is an introductory note

1
00:00:01.000 --> 00:00:04.000 align:start size:50%
<00:00:01.000>When <00:00:02.000>I was young

2
00:00:04.500 --> 00:00:08.000
<c.yellow>I listened to the radio</c>`

	parsed := ParseLyrics(vttInput)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true for WebVTT")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d: %#v", len(cleanLines), cleanLines)
	}
	if cleanLines[0] != "When I was young" || cleanLines[1] != "I listened to the radio" {
		t.Errorf("unexpected clean lines: %#v", cleanLines)
	}

	rebuilt := parsed.RebuildLyrics([]string{"当我年少时", "我常常倾听收音机"})

	if !strings.Contains(rebuilt, "WEBVTT - Demo subtitles") {
		t.Errorf("WEBVTT header missing in rebuilt:\n%s", rebuilt)
	}
	if !strings.Contains(rebuilt, "NOTE\nThis is an introductory note") {
		t.Errorf("NOTE block missing in rebuilt:\n%s", rebuilt)
	}
	if !strings.Contains(rebuilt, "1\n00:00:01.000 --> 00:00:04.000 align:start size:50%\n当我年少时") {
		t.Errorf("cue 1 missing or corrupted in rebuilt:\n%s", rebuilt)
	}
	if !strings.Contains(rebuilt, "2\n00:00:04.500 --> 00:00:08.000\n我常常倾听收音机") {
		t.Errorf("cue 2 missing or corrupted in rebuilt:\n%s", rebuilt)
	}
	if strings.Contains(rebuilt, "<00:00:") || strings.Contains(rebuilt, "<c.yellow>") {
		t.Errorf("inline tags should be stripped in rebuilt:\n%s", rebuilt)
	}
}

// TestParseAndRebuildSRT 验证 SRT (SubRip) 格式
func TestParseAndRebuildSRT(t *testing.T) {
	srtInput := `1
00:00:01,000 --> 00:00:04,000
When I was young

2
00:00:04,500 --> 00:00:08,000
<font color="#ff0000">I listened</font> to the radio`

	parsed := ParseLyrics(srtInput)
	if !parsed.HasTimeline {
		t.Errorf("expected HasTimeline=true for SRT")
	}

	cleanLines := parsed.ExtractCleanLinesForLLM()
	if len(cleanLines) != 2 {
		t.Fatalf("expected 2 clean lines, got %d: %#v", len(cleanLines), cleanLines)
	}
	if cleanLines[0] != "When I was young" || cleanLines[1] != "I listened to the radio" {
		t.Errorf("unexpected clean lines: %#v", cleanLines)
	}

	rebuilt := parsed.RebuildLyrics([]string{"当我年少时", "我常常倾听收音机"})

	expectedBlock1 := "1\n00:00:01,000 --> 00:00:04,000\n当我年少时"
	expectedBlock2 := "2\n00:00:04,500 --> 00:00:08,000\n我常常倾听收音机"
	if !strings.Contains(rebuilt, expectedBlock1) {
		t.Errorf("expected block 1 missing, got:\n%s", rebuilt)
	}
	if !strings.Contains(rebuilt, expectedBlock2) {
		t.Errorf("expected block 2 missing, got:\n%s", rebuilt)
	}
	if strings.Contains(rebuilt, "<font") {
		t.Errorf("html tags should be stripped, got:\n%s", rebuilt)
	}
}

// TestCrossFormatCacheConsistency 验证相同歌词内容在标准LRC、Enhanced LRC、ESLRC、ASS、WebVTT、SRT下生成的缓存Key完全一致
func TestCrossFormatCacheConsistency(t *testing.T) {
	targetLang := "zh"

	standardLrc := "[00:01.00]When I was young\n[00:04.50]I listened to the radio"
	enhancedLrc := "[00:01.00]<00:01.00>When <00:01.50>I was young\n[00:04.50]<00:04.50>I listened <00:05.50>to the radio"
	eslrc := "[00:01.00]When(1000,500) I was young\n[00:04.50]I listened(4500,500) to the radio"
	ass := "[Events]\nDialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,{\\k10}When I was young\nDialogue: 0,0:00:04.50,0:00:08.00,Default,,0,0,0,,{\\kf10}I listened to the radio"
	vtt := "WEBVTT\n\n00:00:01.000 --> 00:00:04.000\nWhen I was young\n\n00:00:04.500 --> 00:00:08.000\nI listened to the radio"
	srt := "1\n00:00:01,000 --> 00:00:04,000\nWhen I was young\n\n2\n00:00:04,500 --> 00:00:08,000\nI listened to the radio"

	kLrc, _ := GenerateCacheKey(standardLrc, targetLang)
	kEnhanced, _ := GenerateCacheKey(enhancedLrc, targetLang)
	kEslrc, _ := GenerateCacheKey(eslrc, targetLang)
	kAss, _ := GenerateCacheKey(ass, targetLang)
	kVtt, _ := GenerateCacheKey(vtt, targetLang)
	kSrt, _ := GenerateCacheKey(srt, targetLang)

	if kLrc != kEnhanced {
		t.Errorf("LRC and Enhanced LRC keys mismatch: %s vs %s", kLrc, kEnhanced)
	}
	if kLrc != kEslrc {
		t.Errorf("LRC and ESLRC keys mismatch: %s vs %s", kLrc, kEslrc)
	}
	if kLrc != kAss {
		t.Errorf("LRC and ASS keys mismatch: %s vs %s", kLrc, kAss)
	}
	if kLrc != kVtt {
		t.Errorf("LRC and WebVTT keys mismatch: %s vs %s", kLrc, kVtt)
	}
	if kLrc != kSrt {
		t.Errorf("LRC and SRT keys mismatch: %s vs %s", kLrc, kSrt)
	}
}

func TestContextInfoExtraction(t *testing.T) {
	// 1. LRC 格式测试
	lrc := "[ti:晴天]\n[ar:周杰伦]\n[00:01.00]故事的小黄花"
	pLrc := ParseLyrics(lrc)
	if pLrc.Title != "晴天" {
		t.Errorf("expected Title='晴天', got '%s'", pLrc.Title)
	}
	if pLrc.Artist != "周杰伦" {
		t.Errorf("expected Artist='周杰伦', got '%s'", pLrc.Artist)
	}
	expectedContext := "【背景信息】歌曲名：《晴天》，歌手/乐队：周杰伦"
	if pLrc.FormatContextInfo() != expectedContext {
		t.Errorf("expected context '%s', got '%s'", expectedContext, pLrc.FormatContextInfo())
	}

	// 2. ASS 格式测试
	ass := "[Script Info]\nTitle: Lemon\nArtist: 米津玄師\n[Events]\nDialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,夢ならばどれほどよかったでしょう"
	pAss := ParseLyrics(ass)
	if pAss.Title != "Lemon" || pAss.Artist != "米津玄師" {
		t.Errorf("expected Title='Lemon', Artist='米津玄師', got '%s', '%s'", pAss.Title, pAss.Artist)
	}
	expectedAssContext := "【背景信息】歌曲名：《Lemon》，歌手/乐队：米津玄師"
	if pAss.FormatContextInfo() != expectedAssContext {
		t.Errorf("expected context '%s', got '%s'", expectedAssContext, pAss.FormatContextInfo())
	}

	// 3. 仅有歌名
	onlyTitle := "[ti:Viva La Vida]\n[00:01.00]I used to rule the world"
	pOnlyTitle := ParseLyrics(onlyTitle)
	if pOnlyTitle.FormatContextInfo() != "【背景信息】歌曲名：《Viva La Vida》" {
		t.Errorf("unexpected context: %s", pOnlyTitle.FormatContextInfo())
	}

	// 4. 仅有歌手
	onlyArtist := "[ar:Queen]\n[00:01.00]Is this the real life"
	pOnlyArtist := ParseLyrics(onlyArtist)
	if pOnlyArtist.FormatContextInfo() != "【背景信息】歌手/乐队：Queen" {
		t.Errorf("unexpected context: %s", pOnlyArtist.FormatContextInfo())
	}

	// 5. 无背景信息
	noMeta := "[00:01.00]Hello world"
	pNoMeta := ParseLyrics(noMeta)
	if pNoMeta.FormatContextInfo() != "" {
		t.Errorf("expected empty context, got '%s'", pNoMeta.FormatContextInfo())
	}
}

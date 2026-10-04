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

package ai

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	for text, want := range map[string]int{
		"":                        0,
		"abcd":                    1,
		"abcde":                   2,
		"服务器磁盘":                   5,
		"df -h 查看磁盘":              6, // 6 bytes of ASCII and a space, 4 wide
		strings.Repeat("x", 4000): 1000,
	} {
		if got := EstimateTokens(text); got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", text, got, want)
		}
	}
	if ContextBudget(0) != DefaultContextTokens || ContextBudget(100) != 8000 || ContextBudget(128000) != 128000 {
		t.Error("ContextBudget")
	}
}

func TestShrinkToolResults(t *testing.T) {
	long := strings.Repeat("line of output\n", 2000)
	messages := []Message{
		{Role: "system", Content: "prompt"},
		{Role: "tool", ToolCallID: "a", Content: long},
		{Role: "tool", ToolCallID: "b", Content: long},
	}
	before := EstimateMessages(messages)
	if !ShrinkToolResults(messages, before-1000) {
		t.Fatal("nothing shrunk over budget")
	}
	// The oldest gives way first; the newest is kept while that is enough.
	if len(messages[1].Content) > 1200 || messages[2].Content != long {
		t.Fatalf("shrunk %d and %d bytes", len(messages[1].Content), len(messages[2].Content))
	}
	if ShrinkToolResults(messages, 1<<30) {
		t.Fatal("shrunk within budget")
	}
}

package app

import (
	"strings"
	"testing"

	"finallyshell/internal/icons"
	"finallyshell/locales"
)

// TestAIPromptCatalog: every category has an icon, a name and tasks in
// both languages, and an ID of its own.
func TestAIPromptCatalog(t *testing.T) {
	seen := map[string]bool{}
	total := 0
	for _, cat := range aiPromptCatalog() {
		if cat.ID == "" || seen[cat.ID] || cat.En == "" || cat.Zh == "" || len(cat.Prompts) < 3 {
			t.Errorf("category %+v", cat.ID)
		}
		seen[cat.ID] = true
		icons.Get(cat.Icon) // panics in a test on a missing icon
		for i, p := range cat.Prompts {
			for _, s := range []string{p.En[0], p.En[1], p.Zh[0], p.Zh[1]} {
				if strings.TrimSpace(s) == "" {
					t.Errorf("%s #%d: an empty title or text", cat.ID, i)
				}
			}
		}
		total += len(cat.Prompts)
	}
	if len(seen) < 12 || total < 60 {
		t.Fatalf("%d categories, %d tasks", len(seen), total)
	}
}

// TestDesktopAIPrompts opens the common tasks, chooses a category,
// searches, and puts a task in the question.
func TestDesktopAIPrompts(t *testing.T) {
	at := newAIAgentTest(t, func(string) string { return aiTextEvent("ok") })
	at.platform.SetLocale("zh-Hans")
	tt := at.platform.tt
	tt.Frame()
	if err := tt.Click(locales.DesktopAIPromptsMore()); err != nil {
		t.Fatalf("%v; %q", err, tt.Texts())
	}
	tt.Frame()
	if !at.state.prompts || !tt.HasText("全面健康检查") {
		t.Fatalf("no tasks: %q", tt.Texts())
	}
	if err := tt.Click("Docker 容器"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if at.state.promptCat != "docker" || !tt.HasText("容器反复重启") || tt.HasText("全面健康检查") {
		t.Fatalf("category: %q", tt.Texts())
	}
	saveShot(t, tt, "ai-prompts-zh-Hans.png")
	at.state.promptCat, at.state.promptQuery = "", "证书"
	tt.Frame()
	if !tt.HasText("申请证书") || tt.HasText("容器反复重启") {
		t.Fatalf("search: %q", tt.Texts())
	}
	if err := tt.Click("申请证书"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if at.state.prompts || !strings.Contains(at.state.draft, "certbot") {
		t.Fatalf("draft %q, open %v", at.state.draft, at.state.prompts)
	}
	// The composer's button opens them too.
	if err := tt.Click(locales.DesktopAIPrompts()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !at.state.prompts || at.state.promptQuery != "" {
		t.Fatal("the composer's button did not open the tasks afresh")
	}
}

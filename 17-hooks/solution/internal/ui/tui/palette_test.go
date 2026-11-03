package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"harness/internal/commands"
	"harness/internal/ui/tui"
)

func typeText(m *tui.Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg(tea.Key{Text: string(r), Code: r}))
	}
}

func TestPaletteFilters(t *testing.T) {
	reg := commands.NewRegistry()
	for _, n := range []string{"compact", "context", "help", "model"} {
		reg.Register(commands.Command{Name: n, Description: "does " + n, Run: func(context.Context, string) (string, error) { return "", nil }})
	}
	m := tui.FromEvents(nil, 100, 30)
	m.SetCommands(reg)
	typeText(m, "/co")
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "/compact") || !strings.Contains(view, "/context") || strings.Contains(view, "/help") {
		t.Errorf("palette should list commands matching /co:\n%s", view)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if v := ansi.Strip(m.View().Content); !strings.Contains(v, "> /compact") {
		t.Errorf("Tab should complete the first match:\n%s", v)
	}
}

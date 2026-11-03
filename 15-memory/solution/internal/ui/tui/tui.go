// Package tui is a full-screen front end. It renders agent events into a
// scrolling transcript and never calls into the loop except to start a run,
// cancel it, steer it, or answer a confirmation.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"harness/internal/agent"
	"harness/internal/commands"
	"harness/internal/llm"
	"harness/internal/permission"
)

// EventMsg delivers one agent event to the UI.
type EventMsg struct{ agent.Event }

type runDoneMsg struct{ err error }

type cmdDoneMsg struct {
	name, out string
	err       error
}

type submitMsg struct{ text string }

type permMsg struct {
	req   permission.Request
	reply chan permission.Decision
}

type cellKind int

const (
	cellUser cellKind = iota
	cellAssistant
	cellThinking
	cellTool
	cellInfo
	cellError
)

type cell struct {
	kind   cellKind
	text   string
	call   *llm.ToolCall
	result *llm.ToolResult
}

type Model struct {
	agent *agent.Agent // nil in replay mode
	model string
	send  func(tea.Msg)
	extra agent.Sink // also receives every event (e.g. a trace recorder)
	cmds  *commands.Registry

	cells   []cell
	tools   map[string]int // call ID → cell index
	vp      viewport.Model
	input   textinput.Model
	w, h    int
	running bool
	cancel  context.CancelFunc
	confirm *permMsg
	status  string // what the agent is doing now

	contextTokens int
	cachedTokens  int
	usable        int
	usage         llm.Usage
	turn          int
}

var (
	dim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	userStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	toolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	barStyle  = lipgloss.NewStyle().Reverse(true)
	askStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
)

func newModel(a *agent.Agent, model string, w, h int) *Model {
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "ask the agent — Enter sends, Esc cancels a run, Ctrl-C quits"
	in.Focus()
	m := &Model{agent: a, model: model, tools: map[string]int{}, input: in, vp: viewport.New()}
	m.resize(w, h)
	return m
}

// New returns a Model that drives a live agent. Call SetSend before use.
func New(a *agent.Agent, model string) *Model { return newModel(a, model, 80, 24) }

func (m *Model) SetSend(f func(tea.Msg)) { m.send = f }

// SetCommands enables slash commands and the command palette.
func (m *Model) SetCommands(r *commands.Registry) { m.cmds = r }

// Submit sends text as if the user typed it; safe from any goroutine
// (prompt templates use it to start a run).
func (m *Model) Submit(text string) { m.send(submitMsg{text}) }

// FromEvents renders a recorded event stream without a live agent.
func FromEvents(events []agent.Event, w, h int) *Model {
	m := newModel(nil, "", w, h)
	for _, e := range events {
		m.Update(EventMsg{e})
	}
	return m
}

func (m *Model) Init() tea.Cmd { return textinput.Blink }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
	case EventMsg:
		m.apply(msg.Event)
	case runDoneMsg:
		m.running, m.cancel, m.status = false, nil, ""
		if msg.err != nil && msg.err != context.Canceled {
			m.cells = append(m.cells, cell{kind: cellError, text: msg.err.Error()})
		}
		if msg.err == context.Canceled {
			m.cells = append(m.cells, cell{kind: cellInfo, text: "cancelled"})
		}
	case permMsg:
		m.confirm = &msg
	case submitMsg:
		if !m.running && m.agent != nil {
			m.start(msg.text)
		}
	case cmdDoneMsg:
		switch {
		case msg.err != nil:
			m.cells = append(m.cells, cell{kind: cellError, text: "/" + msg.name + ": " + msg.err.Error()})
		case msg.out != "":
			m.cells = append(m.cells, cell{kind: cellInfo, text: msg.out})
		}
	case tea.KeyPressMsg:
		if cmd, handled := m.key(msg); handled {
			m.refresh()
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	var vcmd tea.Cmd
	if _, isKey := msg.(tea.KeyPressMsg); !isKey {
		m.vp, vcmd = m.vp.Update(msg)
	}
	m.refresh()
	return m, tea.Batch(cmd, vcmd)
}

func (m *Model) key(k tea.KeyPressMsg) (tea.Cmd, bool) {
	if m.confirm != nil {
		switch k.String() {
		case "y", "Y":
			m.answer(permission.Decision{Action: permission.Allow})
		case "a", "A":
			m.answer(permission.Decision{Action: permission.Allow, Remember: true})
		case "n", "N", "esc", "enter":
			m.answer(permission.Decision{Action: permission.Deny})
		}
		return nil, true
	}
	switch k.String() {
	case "ctrl+c":
		if m.running {
			m.cancel()
			return nil, true
		}
		return tea.Quit, true
	case "esc":
		if m.running {
			m.cancel()
		}
		return nil, true
	case "tab":
		if matches := m.palette(); len(matches) > 0 {
			m.input.SetValue("/" + matches[0].Name + " ")
			m.input.CursorEnd()
		}
		return nil, true
	case "pgup", "pgdown", "up", "down":
		m.vp, _ = m.vp.Update(k)
		return nil, true
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" || m.agent == nil {
			return nil, true
		}
		m.input.Reset()
		if name, args, ok := commands.Parse(text); ok && m.cmds != nil {
			if c, found := m.cmds.Lookup(name); found {
				m.cells = append(m.cells, cell{kind: cellInfo, text: text})
				send := m.send
				go func() {
					out, err := c.Run(context.Background(), args)
					send(cmdDoneMsg{name, out, err})
				}()
				return nil, true
			}
		}
		if m.running {
			m.agent.Steer(text)
			m.cells = append(m.cells, cell{kind: cellInfo, text: "queued: " + text})
			return nil, true
		}
		m.start(text)
		return nil, true
	}
	return nil, false
}

func (m *Model) answer(d permission.Decision) {
	verdict := "denied"
	switch {
	case d.Action == permission.Allow && d.Remember:
		verdict = "always allowed"
	case d.Action == permission.Allow:
		verdict = "allowed"
	}
	m.cells = append(m.cells, cell{kind: cellInfo, text: verdict + ": " + strings.Join(m.confirm.req.Subjects, "; ")})
	m.confirm.reply <- d
	m.confirm = nil
}

func (m *Model) start(text string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.running, m.cancel, m.status = true, cancel, "waiting for model"
	send := m.send
	go func() {
		err := m.agent.Run(ctx, text, agent.Tee(func(e agent.Event) { send(EventMsg{e}) }, m.extra))
		send(runDoneMsg{err})
	}()
}

// Approve implements permission.Approver: it asks through the UI and blocks
// the tool until the user answers or the run is cancelled.
func (m *Model) Approve(ctx context.Context, req permission.Request) permission.Decision {
	reply := make(chan permission.Decision, 1)
	m.send(permMsg{req: req, reply: reply})
	select {
	case d := <-reply:
		return d
	case <-ctx.Done():
		return permission.Decision{Action: permission.Deny}
	}
}

func (m *Model) apply(e agent.Event) {
	switch e.Kind {
	case agent.AgentStart:
		m.cells = append(m.cells, cell{kind: cellUser, text: e.Text})
	case agent.Steer:
		m.cells = append(m.cells, cell{kind: cellUser, text: e.Text})
	case agent.TurnStart:
		m.turn, m.status = e.Turn, "waiting for model"
	case agent.ThinkingDelta:
		m.appendText(cellThinking, e.Text)
		m.status = "thinking"
	case agent.TextDelta:
		m.appendText(cellAssistant, e.Text)
		m.status = "writing"
	case agent.ToolCallStart:
		m.status = "writing " + e.Call.Name + " call"
	case agent.Compaction:
		before, _ := num(e.Data["before"])
		after, _ := num(e.Data["after"])
		m.cells = append(m.cells, cell{kind: cellInfo, text: fmt.Sprintf("compacted %d messages into %d (%v)", before, after, e.Data["reason"])})
	case agent.ContextInfo:
		if u, ok := num(e.Data["usable"]); ok {
			m.usable = u
		}
		if p, ok := num(e.Data["pruned_tokens"]); ok && p > 0 {
			m.cells = append(m.cells, cell{kind: cellInfo, text: "pruned " + kTokens(p) + " tokens of old tool output"})
		}
	case agent.MessageEnd:
		if e.Usage != nil {
			m.contextTokens, m.cachedTokens = e.Usage.Input, e.Usage.CacheRead
		}
	case agent.ToolStart:
		m.tools[e.Call.ID] = len(m.cells)
		m.cells = append(m.cells, cell{kind: cellTool, call: e.Call})
		m.status = "running " + e.Call.Name
	case agent.ToolEnd:
		if i, ok := m.tools[e.Call.ID]; ok {
			m.cells[i].result = e.Result
		}
	case agent.Error:
		m.cells = append(m.cells, cell{kind: cellError, text: e.Text})
	case agent.AgentEnd:
		if e.Usage != nil {
			m.usage.Add(*e.Usage)
		}
		m.status = ""
	}
}

func (m *Model) appendText(kind cellKind, s string) {
	if n := len(m.cells); n > 0 && m.cells[n-1].kind == kind {
		m.cells[n-1].text += s
		return
	}
	m.cells = append(m.cells, cell{kind: kind, text: s})
}

func (m *Model) resize(w, h int) {
	m.w, m.h = w, h
	m.vp.SetWidth(w)
	m.vp.SetHeight(max(h-3, 1))
	m.input.SetWidth(max(w-3, 1))
	m.refresh()
}

func (m *Model) refresh() {
	follow := m.vp.AtBottom() || m.vp.TotalLineCount() <= m.vp.Height()
	m.vp.SetContent(m.render(m.w))
	if follow {
		m.vp.GotoBottom()
	}
}

// Transcript is the full rendered conversation at the current width.
func (m *Model) Transcript() string { return m.render(m.w) }

func (m *Model) render(width int) string {
	wrap := lipgloss.NewStyle().Width(max(width, 10))
	var b strings.Builder
	for _, c := range m.cells {
		var s string
		switch c.kind {
		case cellUser:
			s = userStyle.Render("you") + "\n" + c.text
		case cellAssistant:
			s = strings.TrimSpace(c.text)
		case cellThinking:
			s = dim.Render(strings.TrimSpace(c.text))
		case cellInfo:
			s = dim.Render("· " + c.text)
		case cellError:
			s = errStyle.Render("error: " + c.text)
		case cellTool:
			s = toolCell(c)
		}
		b.WriteString(wrap.Render(s))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func toolCell(c cell) string {
	head := toolStyle.Render("→ " + c.call.Name + " " + argSummary(c.call.Args))
	if c.result == nil {
		return head + dim.Render("  …")
	}
	lines := strings.Split(strings.TrimRight(c.result.Content, "\n"), "\n")
	more := ""
	if len(lines) > 4 {
		more = fmt.Sprintf("\n  … %d more lines", len(lines)-4)
		lines = lines[:4]
	}
	body := "  " + strings.Join(lines, "\n  ") + more
	if c.result.IsError {
		return head + " " + errStyle.Render("✗") + "\n" + errStyle.Render(body)
	}
	return head + " " + dim.Render("✓") + "\n" + dim.Render(body)
}

// num reads an int from event data, which may have come through JSON (float64).
func num(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

func kTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 100000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.0fk", float64(n)/1000)
}

// argSummary renders tool arguments as "k=v ..." (bash shows its command).
func argSummary(raw json.RawMessage) string {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return oneLine(string(raw), 200)
	}
	if cmd, ok := args["command"].(string); ok {
		return oneLine(cmd, 200)
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprint(args[k])
		if b, err := json.Marshal(args[k]); err == nil && !isString(args[k]) {
			v = string(b)
		}
		parts = append(parts, k+"="+v)
	}
	return oneLine(strings.Join(parts, " "), 200)
}

func isString(v any) bool { _, ok := v.(string); return ok }

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > n {
		return string([]rune(s)[:n]) + "…"
	}
	return s
}

// Status is the one-line summary under the transcript.
func (m *Model) Status() string {
	state := "idle"
	switch {
	case m.confirm != nil:
		state = "waiting for you"
	case m.status != "":
		state = m.status
	}
	ctx := fmt.Sprintf("context %d tok", m.contextTokens)
	if m.usable > 0 {
		ctx = fmt.Sprintf("context %s/%s (%d%%)", kTokens(m.contextTokens), kTokens(m.usable), m.contextTokens*100/m.usable)
	}
	if m.contextTokens > 0 {
		ctx += fmt.Sprintf(" · cache %d%%", m.cachedTokens*100/m.contextTokens)
	}
	return fmt.Sprintf(" %s · turn %d · %s · session $%.4f · %s ", m.model, m.turn, ctx, m.usage.CostUSD, state)
}

// palette returns commands matching the input while a command name is being typed.
func (m *Model) palette() []commands.Command {
	v := m.input.Value()
	if m.cmds == nil || !strings.HasPrefix(v, "/") || strings.Contains(v, " ") {
		return nil
	}
	return m.cmds.Complete(strings.TrimPrefix(v, "/"))
}

func (m *Model) View() tea.View {
	var bottom string
	if items := m.palette(); len(items) > 0 && m.confirm == nil {
		var lines []string
		for i, c := range items {
			if i == 6 {
				break
			}
			lines = append(lines, toolStyle.Render(fmt.Sprintf("  /%-14s", c.Name))+dim.Render(" "+c.Description))
		}
		bottom = strings.Join(lines, "\n") + "\n"
	}
	if m.confirm != nil {
		bottom = askStyle.Render("Allow "+m.confirm.req.Tool+"? [y]es [a]lways [n]o ") + oneLine(strings.Join(m.confirm.req.Subjects, "; "), max(m.w-36, 10))
	} else {
		bottom += m.input.View()
	}
	status := barStyle.Render(lipgloss.NewStyle().Width(m.w).MaxWidth(m.w).Render(m.Status()))
	v := tea.NewView(m.vp.View() + "\n" + status + "\n" + lipgloss.NewStyle().MaxWidth(m.w).Render(bottom))
	v.AltScreen = true
	return v
}

// Run starts the full-screen UI for a live agent and returns when the user
// quits. extra (may be nil) receives every event as well.
func Run(a *agent.Agent, model string, extra agent.Sink) error {
	return RunWith(New(a, model), extra)
}

// RunWith runs a prepared Model (e.g. one with SetCommands applied).
func RunWith(m *Model, extra agent.Sink) error {
	a := m.agent
	m.extra = extra
	p := tea.NewProgram(m)
	m.SetSend(p.Send)
	if env := a.Tools().Env(); env.Perm != nil {
		env.Perm.SetApprover(m)
	}
	_, err := p.Run()
	return err
}

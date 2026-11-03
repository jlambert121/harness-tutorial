package ctxmgr

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"harness/internal/llm"
)

//go:embed prompts/*.md
var prompts embed.FS

func prompt(name string) string {
	b, _ := prompts.ReadFile("prompts/" + name)
	return strings.TrimSpace(string(b))
}

var ErrNothingToCompact = errors.New("nothing to compact: the conversation fits in the recent window")

// Compactor replaces old history with a model-written summary, keeping the
// most recent KeepRecent tokens verbatim.
type Compactor struct {
	Client     llm.Client
	Model      string
	KeepRecent int
	Cache      llm.CacheMode // use the session's mode so the prefix matches what was cached
}

func NewCompactor(client llm.Client, model string, keepRecent int) *Compactor {
	return &Compactor{Client: client, Model: model, KeepRecent: keepRecent}
}

type CompactOptions struct {
	Focus       string   // extra instructions, e.g. from /compact <focus>
	Context     string   // environment and project instructions to re-inject
	RecentFiles []string // absolute paths re-attached with their current content
}

type CompactResult struct {
	Summary string
	History []llm.Message // summary message followed by the kept tail
	Cut     int           // number of original messages summarized
	Usage   *llm.Usage
}

// SplitPoint returns the index where the kept tail begins: walking back from
// the end until keepTokens are covered, then forward past any tool results,
// so the tail never starts with a result whose call was summarized away (pi's
// rule: never cut at a tool result). The cut may fall inside a single long
// turn, which is what a headless run with one prompt needs. 0 means there is
// nothing to summarize.
func SplitPoint(msgs []llm.Message, keepTokens int) int {
	kept := 0
	i := len(msgs)
	for i > 0 && kept < keepTokens {
		i--
		kept += (messageBytes(msgs[i]) + 3) / 4
	}
	if kept < keepTokens {
		return 0
	}
	for i < len(msgs) && msgs[i].Role == llm.RoleTool {
		i++
	}
	if i >= len(msgs) || i == 0 {
		return 0
	}
	return i
}

var priorRe = regexp.MustCompile(`(?s)<conversation-summary>\n?(.*?)\n?</conversation-summary>`)

// Compact summarizes everything before the split point. The request reuses
// the session's system prompt, tools, and message prefix unchanged and adds
// the instructions as a final user message, so the provider can serve the
// prefix from cache (the pattern Claude Code uses). Tools stay defined but
// tool_choice is "none".
func (c *Compactor) Compact(ctx context.Context, system string, tools []llm.ToolSpec, msgs []llm.Message, opts CompactOptions) (*CompactResult, error) {
	split := SplitPoint(msgs, c.KeepRecent)
	if split == 0 {
		return nil, ErrNothingToCompact
	}
	head, tail := msgs[:split], msgs[split:]

	instructions := prompt("compact.md")
	if m := priorRe.FindStringSubmatch(head[0].Text()); m != nil {
		instructions = prompt("update.md") + "\n\n<prior-summary>\n" + m[1] + "\n</prior-summary>\n\n" + instructions
	}
	if opts.Focus != "" {
		instructions += "\n\nThe user asked the summary to focus on: " + opts.Focus
	}
	req := llm.Request{
		Model: c.Model, System: system, Tools: tools,
		Messages:  append(append([]llm.Message(nil), head...), llm.UserText(instructions)),
		Extra:     map[string]any{"tool_choice": "none"},
		Cache:     c.Cache,
		MaxTokens: min(4096, max(c.KeepRecent, 1024)),
	}
	var msg *llm.Message
	for ev := range c.Client.Stream(ctx, req) {
		if ev.Type == llm.EventDone {
			msg = ev.Message
		}
	}
	if msg.Stop == llm.StopError || msg.Stop == llm.StopAborted {
		return nil, fmt.Errorf("compaction failed: %s", msg.Err)
	}
	summary := strings.TrimSpace(msg.Text())
	if summary == "" {
		return nil, errors.New("compaction failed: empty summary")
	}

	var b strings.Builder
	if opts.Context != "" {
		b.WriteString(opts.Context + "\n\n")
	}
	fmt.Fprintf(&b, "<conversation-summary>\n%s\n</conversation-summary>\n\nThe conversation above was compacted; the summary replaces %d earlier messages. The most recent messages follow unchanged.", summary, split)
	b.WriteString(c.recentFiles(opts.RecentFiles, min(5000, c.KeepRecent/4), c.KeepRecent/2))
	history := append([]llm.Message{llm.UserText(b.String())}, tail...)
	return &CompactResult{Summary: summary, History: history, Cut: split, Usage: msg.Usage}, nil
}

// recentFiles re-attaches up to 5 recently used files with their current
// content (Claude Code's rule: at most 5 files, 5k tokens each). perFile and
// total scale those limits down for small windows, where re-attaching could
// refill the context that compaction just freed. Larger files are listed by
// path only.
func (c *Compactor) recentFiles(paths []string, perFile, total int) string {
	var b strings.Builder
	n, used := 0, 0
	for _, p := range paths {
		if n == 5 {
			break
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		n++
		if t := (len(data) + 3) / 4; t > perFile || used+t > total {
			fmt.Fprintf(&b, "\n\n<file path=%q>(%d bytes; too large to re-attach, read it if needed)</file>", p, len(data))
			continue
		}
		used += (len(data) + 3) / 4
		fmt.Fprintf(&b, "\n\n<file path=%q name=%q>\n%s\n</file>", p, filepath.Base(p), data)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nRecently used files, current content:" + b.String()
}

var overflowRe = regexp.MustCompile(`(?i)prompt is too long|maximum context length|context window|context length exceeded|too many tokens`)

// IsOverflow recognizes provider errors that mean the request was too big.
func IsOverflow(err string) bool { return overflowRe.MatchString(err) }

// Need reports whether a request of est tokens should trigger compaction:
// over 90% of usable context (Codex's threshold).
func (m *Manager) Need(est int) bool { return float64(est) > 0.9*float64(m.Budget.Usable()) }

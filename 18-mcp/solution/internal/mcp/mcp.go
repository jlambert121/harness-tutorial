// Package mcp is a minimal Model Context Protocol client over stdio, plus an
// adapter that exposes a server's tools in the harness registry.
package mcp

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"harness/internal/llm"
	"harness/internal/tools"
)

// ProtocolVersion is the MCP revision this client speaks: 2025-11-25, the
// last revision with the initialize handshake. 2026-07-28 replaced the
// handshake with per-request _meta and server/discover (see the lesson).
const ProtocolVersion = "2025-11-25"

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type Client struct {
	name         string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	nextID       atomic.Int64
	mu           sync.Mutex
	pending      map[int64]chan response
	dead         chan struct{}
	deadErr      error
	writeMu      sync.Mutex
	serverName   string
	instructions string
}

// Start launches the server, performs the initialize handshake, and sends
// notifications/initialized.
func Start(ctx context.Context, name string, argv []string, env []string) (*Client, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}
	c := &Client{name: name, cmd: cmd, stdin: stdin, pending: map[int64]chan response{}, dead: make(chan struct{})}
	go c.readLoop(stdout)

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Instructions string `json:"instructions"`
	}
	err = c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "harness", "version": "0.18"},
	}, &init)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}
	c.serverName, c.instructions = init.ServerInfo.Name, init.Instructions
	if err := c.notify("notifications/initialized", nil); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) ServerName() string   { return c.serverName }
func (c *Client) Instructions() string { return c.instructions }

// readLoop routes responses to waiting calls. When the server exits, every
// pending and future call fails instead of hanging.
func (c *Client) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var resp response
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.ID == nil {
			continue // notifications and server-initiated requests are ignored here
		}
		c.mu.Lock()
		ch := c.pending[*resp.ID]
		delete(c.pending, *resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	err := c.cmd.Wait()
	c.mu.Lock()
	c.deadErr = fmt.Errorf("mcp server %s exited: %v", c.name, err)
	c.mu.Unlock()
	close(c.dead)
}

func (c *Client) send(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *Client) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.send(msg)
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("%s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
		}
		if out != nil {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-c.dead:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.deadErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	ReadOnly    bool            `json:"-"`
}

// ListTools follows nextCursor until the server has returned every tool.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var all []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Tools []struct {
				Tool
				Annotations struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := c.call(ctx, "tools/list", params, &res); err != nil {
			return nil, err
		}
		for _, t := range res.Tools {
			t.Tool.ReadOnly = t.Annotations.ReadOnlyHint
			all = append(all, t.Tool)
		}
		if res.NextCursor == "" {
			return all, nil
		}
		cursor = res.NextCursor
	}
}

type CallResult struct {
	Text    string
	IsError bool
}

func (c *Client) Call(ctx context.Context, tool string, args json.RawMessage) (CallResult, error) {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": args}, &res); err != nil {
		return CallResult{}, err
	}
	var parts []string
	for _, p := range res.Content {
		if p.Type == "text" {
			parts = append(parts, p.Text)
		} else {
			parts = append(parts, "["+p.Type+" content omitted]")
		}
	}
	return CallResult{Text: strings.Join(parts, "\n"), IsError: res.IsError}, nil
}

func (c *Client) Close() error {
	c.stdin.Close()
	select {
	case <-c.dead:
	default:
		if c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
		<-c.dead
	}
	return nil
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// ToolName is "mcp__<server>__<tool>" with unsafe characters replaced.
// Names longer than 64 characters (Anthropic's limit) are cut and given a
// short hash suffix so different long names stay distinct (Codex does the same).
func ToolName(server, tool string) string {
	n := "mcp__" + unsafeChars.ReplaceAllString(server, "_") + "__" + unsafeChars.ReplaceAllString(tool, "_")
	if len(n) <= 64 {
		return n
	}
	sum := sha1.Sum([]byte(n))
	return n[:55] + "_" + hex.EncodeToString(sum[:4])
}

// Register adds the server's tools to reg. With deferred, only a
// tool_search tool is added; it registers matching tools when the model
// searches for them, so their definitions cost context only once needed.
func Register(ctx context.Context, reg *tools.Registry, server string, c *Client, deferred bool) (int, error) {
	list, err := c.ListTools(ctx)
	if err != nil {
		return 0, err
	}
	adapt := func(t Tool) tools.Tool {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		return tools.Func{
			Def: llm.ToolSpec{Name: ToolName(server, t.Name), Description: t.Description, Schema: schema},
			RO:  t.ReadOnly,
			Fn: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
				res, err := c.Call(ctx, t.Name, args)
				if err != nil {
					return tools.Result{IsError: true, Content: err.Error()}
				}
				return tools.Result{Content: res.Text, IsError: res.IsError}
			},
		}
	}
	if !deferred {
		for _, t := range list {
			reg.Register(adapt(t))
		}
		return len(list), nil
	}
	searchersMu.Lock()
	sr, ok := searchers[reg]
	if !ok {
		sr = &searcher{reg: reg}
		searchers[reg] = sr
		reg.Register(sr.tool())
	}
	searchersMu.Unlock()
	sr.add(server, list, adapt)
	return len(list), nil
}

// One tool_search serves every deferred server registered in a registry.
var (
	searchersMu sync.Mutex
	searchers   = map[*tools.Registry]*searcher{}
)

type entry struct {
	tool  Tool
	adapt func(Tool) tools.Tool
}

type searcher struct {
	mu      sync.Mutex
	reg     *tools.Registry
	servers []string
	entries []entry
}

func (s *searcher) add(server string, list []Tool, adapt func(Tool) tools.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers = append(s.servers, server)
	for _, t := range list {
		s.entries = append(s.entries, entry{t, adapt})
	}
}

type searchArgs struct {
	Query string `json:"query" desc:"Words describing the capability you need, e.g. \"todo list\""`
}

func (s *searcher) tool() tools.Tool {
	desc := "Find and enable tools from MCP servers (their definitions are not loaded until found). Matching tools become callable in your next step."
	return tools.New("tool_search", desc, false, func(ctx context.Context, env *tools.Env, a searchArgs) tools.Result {
		words := strings.Fields(strings.ToLower(a.Query))
		if len(words) == 0 {
			return tools.Result{IsError: true, Content: "query is empty"}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		var found []string
		for _, e := range s.entries {
			hay := strings.ToLower(e.tool.Name + " " + e.tool.Description)
			match := true
			for _, w := range words {
				if !strings.Contains(hay, w) {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			t := e.adapt(e.tool)
			if _, ok := s.reg.Get(t.Spec().Name); !ok {
				s.reg.Register(t)
			}
			found = append(found, fmt.Sprintf("- %s: %s", t.Spec().Name, e.tool.Description))
			if len(found) == 5 {
				break
			}
		}
		if len(found) == 0 {
			return tools.Result{Content: "No tools match " + a.Query}
		}
		return tools.Result{Content: "Enabled:\n" + strings.Join(found, "\n")}
	})
}

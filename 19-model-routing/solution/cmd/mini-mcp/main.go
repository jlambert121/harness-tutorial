// Command mini-mcp is a small MCP server over stdio: a todo list and a word
// counter. --synthetic N adds N filler tools to measure definition overhead.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func main() {
	synthetic := flag.Int("synthetic", 0, "add this many filler tools")
	crashAfter := flag.Int("crash-after", 0, "exit after this many tools/call requests (tests)")
	flag.Parse()

	tools := []tool{
		{Name: "todo_add", Description: "Add an item to the shared todo list.", InputSchema: obj(map[string]any{"text": map[string]any{"type": "string", "description": "The todo item"}}, "text")},
		{Name: "todo_list", Description: "List all todo items, numbered.", InputSchema: obj(map[string]any{}), Annotations: map[string]any{"readOnlyHint": true}},
		{Name: "word_count", Description: "Count the words in a text.", InputSchema: obj(map[string]any{"text": map[string]any{"type": "string"}}, "text"), Annotations: map[string]any{"readOnlyHint": true}},
	}
	for i := 0; i < *synthetic; i++ {
		tools = append(tools, tool{
			Name: fmt.Sprintf("analytics_report_%02d", i),
			Description: fmt.Sprintf("Generate analytics report variant %d for a workspace. Supports filtering by date range, team, project, "+
				"and label; grouping by week or month; and output as table, CSV, or chart specification. Use only when the user asks for analytics.", i),
			InputSchema: obj(map[string]any{
				"workspace": map[string]any{"type": "string", "description": "Workspace identifier"},
				"from":      map[string]any{"type": "string", "description": "Start date, YYYY-MM-DD"},
				"to":        map[string]any{"type": "string", "description": "End date, YYYY-MM-DD"},
				"group_by":  map[string]any{"type": "string", "enum": []string{"week", "month"}},
				"format":    map[string]any{"type": "string", "enum": []string{"table", "csv", "chart"}},
				"labels":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}, "workspace", "from", "to"),
		})
	}

	var todos []string
	calls := 0
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	fail := func(id json.RawMessage, code int, msg string) {
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	}
	text := func(s string, isErr bool) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}, "isError": isErr}
	}
	for in.Scan() {
		var r request
		if err := json.Unmarshal(in.Bytes(), &r); err != nil {
			fmt.Fprintln(os.Stderr, "mini-mcp: bad message:", err)
			continue
		}
		if r.ID == nil {
			continue // notifications (e.g. notifications/initialized) need no reply
		}
		switch r.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(r.Params, &p)
			reply(r.ID, map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "mini-mcp", "version": "1.0.0"},
				"instructions":    "A shared todo list. Use todo_list before todo_add to avoid duplicates.",
			})
		case "tools/list":
			reply(r.ID, map[string]any{"tools": tools})
		case "tools/call":
			calls++
			if *crashAfter > 0 && calls > *crashAfter {
				os.Exit(3)
			}
			var p struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			json.Unmarshal(r.Params, &p)
			switch p.Name {
			case "todo_add":
				if strings.TrimSpace(p.Arguments["text"]) == "" {
					reply(r.ID, text("text is required", true))
					continue
				}
				todos = append(todos, p.Arguments["text"])
				reply(r.ID, text(fmt.Sprintf("added #%d", len(todos)), false))
			case "todo_list":
				var b strings.Builder
				for i, t := range todos {
					fmt.Fprintf(&b, "%d. %s\n", i+1, t)
				}
				if b.Len() == 0 {
					b.WriteString("(empty)")
				}
				reply(r.ID, text(b.String(), false))
			case "word_count":
				reply(r.ID, text(fmt.Sprint(len(strings.Fields(p.Arguments["text"]))), false))
			default:
				if strings.HasPrefix(p.Name, "analytics_report_") {
					reply(r.ID, text("no analytics data in this demo", false))
					continue
				}
				fail(r.ID, -32602, "unknown tool "+p.Name)
			}
		default:
			fail(r.ID, -32601, "method not found: "+r.Method)
		}
	}
}

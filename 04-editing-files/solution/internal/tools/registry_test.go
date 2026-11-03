package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"harness/internal/llm"
	"harness/internal/tools"
)

func echoTool(name string) tools.Tool {
	return tools.Func{
		Def: llm.ToolSpec{Name: name, Description: "echo", Schema: json.RawMessage(`{"type":"object"}`)},
		RO:  true,
		Fn: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
			return tools.Result{Content: name + ":" + string(args)}
		},
	}
}

func TestSpecsSortedByName(t *testing.T) {
	r := tools.NewRegistry(&tools.Env{})
	r.Register(echoTool("zeta"))
	r.Register(echoTool("alpha"))
	r.Register(echoTool("mid"))
	var names []string
	for _, s := range r.Specs() {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "alpha,mid,zeta" {
		t.Errorf("Specs order = %v; want sorted (stable request prefix)", names)
	}
}

func TestExecuteResultsInCallOrder(t *testing.T) {
	r := tools.NewRegistry(&tools.Env{})
	r.Register(echoTool("a"))
	r.Register(echoTool("b"))
	calls := []llm.ToolCall{
		{ID: "1", Name: "b", Args: json.RawMessage(`{}`)},
		{ID: "2", Name: "a", Args: json.RawMessage(`{"x":1}`)},
	}
	var started, ended []string
	res := r.Execute(context.Background(), calls,
		func(c llm.ToolCall) { started = append(started, c.ID) },
		func(c llm.ToolCall, res llm.ToolResult) { ended = append(ended, c.ID) })
	if len(res) != 2 || res[0].CallID != "1" || res[0].Content != "b:{}" || res[1].CallID != "2" || res[1].Name != "a" {
		t.Errorf("results = %+v", res)
	}
	if strings.Join(started, ",") != "1,2" || len(ended) != 2 {
		t.Errorf("callbacks started=%v ended=%v; onStart fires in call order, onEnd once per call", started, ended)
	}
}

func TestUnknownToolListsAvailable(t *testing.T) {
	r := tools.NewRegistry(&tools.Env{})
	r.Register(echoTool("read_file"))
	res := r.Execute(context.Background(), []llm.ToolCall{{ID: "1", Name: "nope"}}, nil, nil)
	if !res[0].IsError || !strings.Contains(res[0].Content, `"nope"`) || !strings.Contains(res[0].Content, "read_file") {
		t.Errorf("result = %+v; want an error naming the bad tool and listing available ones", res[0])
	}
}

func TestExecuteAfterCancelReturnsAborted(t *testing.T) {
	r := tools.NewRegistry(&tools.Env{})
	r.Register(echoTool("a"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := r.Execute(ctx, []llm.ToolCall{{ID: "1", Name: "a"}}, nil, nil)
	if !res[0].IsError || !strings.Contains(res[0].Content, "aborted") {
		t.Errorf("result = %+v", res[0])
	}
}

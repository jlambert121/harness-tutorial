package tools_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"harness/internal/llm"
	"harness/internal/tools"
)

type span struct{ start, end time.Time }

func timed(name string, readOnly bool, d time.Duration, mu *sync.Mutex, log map[string]span) tools.Tool {
	return tools.Func{
		Def: llm.ToolSpec{Name: name, Schema: json.RawMessage(`{"type":"object"}`)},
		RO:  readOnly,
		Fn: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
			s := time.Now()
			time.Sleep(d)
			mu.Lock()
			log[name] = span{s, time.Now()}
			mu.Unlock()
			return tools.Result{Content: name}
		},
	}
}

func TestParallelReadOnlyOverlap(t *testing.T) {
	var mu sync.Mutex
	log := map[string]span{}
	r := tools.NewRegistry(&tools.Env{})
	r.Register(timed("r1", true, 200*time.Millisecond, &mu, log))
	r.Register(timed("r2", true, 200*time.Millisecond, &mu, log))
	start := time.Now()
	res := r.Execute(context.Background(), []llm.ToolCall{{ID: "a", Name: "r1"}, {ID: "b", Name: "r2"}}, nil, nil)
	if el := time.Since(start); el > 350*time.Millisecond {
		t.Errorf("two read-only tools took %v; they should run concurrently", el)
	}
	if res[0].Content != "r1" || res[1].Content != "r2" {
		t.Errorf("results out of call order: %+v", res)
	}
}

func TestMutatingToolRunsAlone(t *testing.T) {
	var mu sync.Mutex
	log := map[string]span{}
	r := tools.NewRegistry(&tools.Env{})
	r.Register(timed("r1", true, 100*time.Millisecond, &mu, log))
	r.Register(timed("w", false, 100*time.Millisecond, &mu, log))
	r.Register(timed("r2", true, 100*time.Millisecond, &mu, log))
	r.Execute(context.Background(), []llm.ToolCall{{ID: "a", Name: "r1"}, {ID: "b", Name: "w"}, {ID: "c", Name: "r2"}}, nil, nil)
	if log["w"].start.Before(log["r1"].end) {
		t.Error("mutating tool started before the earlier read finished")
	}
	if log["r2"].start.Before(log["w"].end) {
		t.Error("later read started before the mutating tool finished")
	}
}

func TestSequentialMode(t *testing.T) {
	var mu sync.Mutex
	log := map[string]span{}
	r := tools.NewRegistry(&tools.Env{})
	r.Parallel = false
	r.Register(timed("r1", true, 100*time.Millisecond, &mu, log))
	r.Register(timed("r2", true, 100*time.Millisecond, &mu, log))
	r.Execute(context.Background(), []llm.ToolCall{{ID: "a", Name: "r1"}, {ID: "b", Name: "r2"}}, nil, nil)
	if log["r2"].start.Before(log["r1"].end) {
		t.Error("Parallel=false must run calls one at a time")
	}
}

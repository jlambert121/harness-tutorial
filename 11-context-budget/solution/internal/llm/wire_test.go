package llm

import (
	"encoding/json"
	"testing"
)

func TestEncodeRequest(t *testing.T) {
	raw := json.RawMessage(`[{"type":"reasoning.text","text":"","signature":"abc","format":"anthropic-claude-v1","index":0}]`)
	req := Request{
		Model:  "anthropic/claude-haiku-5.5",
		System: "You are a test.",
		Messages: []Message{
			UserText("read a.go"),
			{Role: RoleAssistant, Blocks: []Block{
				{Thinking: &Thinking{Raw: raw}},
				{Text: "Reading."},
				{ToolCall: &ToolCall{ID: "call_1", Name: "read", Args: json.RawMessage(`{"path":"a.go"}`)}},
			}},
			{Role: RoleTool, Result: &ToolResult{CallID: "call_1", Name: "read", Content: "package a"}},
		},
		Tools: []ToolSpec{{
			Name: "read", Description: "Read a file",
			Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		}},
		MaxTokens: 1000,
		Reasoning: "low",
		Extra:     map[string]any{"providerOptions": map[string]any{"gateway": map[string]any{"caching": "auto"}}},
	}
	body, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model         string         `json:"model"`
		Stream        bool           `json:"stream"`
		StreamOptions map[string]any `json:"stream_options"`
		MaxTokens     int            `json:"max_tokens"`
		Reasoning     map[string]any `json:"reasoning"`
		Provider      map[string]any `json:"providerOptions"`
		Messages      []struct {
			Role             string          `json:"role"`
			Content          any             `json:"content"`
			ToolCallID       string          `json:"tool_call_id"`
			ReasoningDetails json.RawMessage `json:"reasoning_details"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != req.Model || !got.Stream || got.StreamOptions["include_usage"] != true || got.MaxTokens != 1000 {
		t.Errorf("top level wrong: %s", body)
	}
	if got.Reasoning["effort"] != "low" {
		t.Errorf("reasoning = %v", got.Reasoning)
	}
	if got.Provider["gateway"].(map[string]any)["caching"] != "auto" {
		t.Errorf("Extra not merged: %v", got.Provider)
	}
	if len(got.Messages) != 4 {
		t.Fatalf("want 4 wire messages (system, user, assistant, tool), got %d: %s", len(got.Messages), body)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "You are a test." {
		t.Errorf("system message = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "read a.go" {
		t.Errorf("user message = %+v", got.Messages[1])
	}
	a := got.Messages[2]
	if a.Role != "assistant" || a.Content != "Reading." || len(a.ToolCalls) != 1 {
		t.Fatalf("assistant message = %+v", a)
	}
	if tc := a.ToolCalls[0]; tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "read" || tc.Function.Arguments != `{"path":"a.go"}` {
		t.Errorf("tool call = %+v (arguments must be a JSON string)", tc)
	}
	if string(a.ReasoningDetails) != string(raw) {
		t.Errorf("reasoning_details must be replayed verbatim:\n got %s\nwant %s", a.ReasoningDetails, raw)
	}
	if m := got.Messages[3]; m.Role != "tool" || m.ToolCallID != "call_1" || m.Content != "package a" {
		t.Errorf("tool message = %+v", m)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "read" || len(got.Tools[0].Function.Parameters) == 0 {
		t.Errorf("tools = %+v", got.Tools)
	}
}

func TestEncodeRequestOmitsEmptyOptionals(t *testing.T) {
	body, err := EncodeRequest(Request{Model: "m", Messages: []Message{UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	for _, k := range []string{"tools", "max_tokens", "reasoning"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s should be omitted when unset: %s", k, body)
		}
	}
	if msgs := got["messages"].([]any); len(msgs) != 1 {
		t.Errorf("no system message expected when System is empty: %s", body)
	}
}

func TestEncodeRequestEmptyArgsAndEnabledReasoning(t *testing.T) {
	body, _ := EncodeRequest(Request{Model: "m", Reasoning: "enabled", Messages: []Message{
		UserText("x"),
		{Role: RoleAssistant, Blocks: []Block{{ToolCall: &ToolCall{ID: "c", Name: "ls"}}}},
	}})
	var got struct {
		Reasoning map[string]any `json:"reasoning"`
		Messages  []struct {
			Content   any `json:"content"`
			ToolCalls []struct {
				Function struct{ Arguments string } `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	json.Unmarshal(body, &got)
	if got.Reasoning["enabled"] != true {
		t.Errorf("reasoning = %v", got.Reasoning)
	}
	if args := got.Messages[1].ToolCalls[0].Function.Arguments; args != "{}" {
		t.Errorf("empty Args must encode as \"{}\", got %q", args)
	}
	if got.Messages[1].Content != nil {
		t.Errorf("assistant with only tool calls should send content null, got %v", got.Messages[1].Content)
	}
}

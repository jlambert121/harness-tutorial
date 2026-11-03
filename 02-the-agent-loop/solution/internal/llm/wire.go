package llm

import (
	"encoding/json"
	"strings"
)

// Wire types for the OpenAI-compatible Chat Completions API.

type wireMessage struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"`
	ToolCalls        []wireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
}

type wireToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// EncodeRequest renders a neutral Request as a streaming Chat Completions body.
func EncodeRequest(req Request) ([]byte, error) {
	body := map[string]any{
		"model":          req.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	var msgs []wireMessage
	if req.System != "" {
		msgs = append(msgs, wireMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, encodeMessage(m))
	}
	body["messages"] = msgs
	if len(req.Tools) > 0 {
		tools := make([]wireTool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i].Type = "function"
			tools[i].Function.Name = t.Name
			tools[i].Function.Description = t.Description
			tools[i].Function.Parameters = t.Schema
		}
		body["tools"] = tools
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	switch req.Reasoning {
	case "":
	case "enabled":
		body["reasoning"] = map[string]any{"enabled": true}
	default:
		body["reasoning"] = map[string]any{"effort": req.Reasoning}
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return json.Marshal(body)
}

func encodeMessage(m Message) wireMessage {
	switch m.Role {
	case RoleTool:
		r := m.Result
		if r == nil {
			r = &ToolResult{}
		}
		return wireMessage{Role: "tool", ToolCallID: r.CallID, Content: r.Content}
	case RoleAssistant:
		w := wireMessage{Role: "assistant"}
		var text strings.Builder
		var details []json.RawMessage
		for _, b := range m.Blocks {
			switch {
			case b.ToolCall != nil:
				var tc wireToolCall
				tc.ID, tc.Type = b.ToolCall.ID, "function"
				tc.Function.Name = b.ToolCall.Name
				tc.Function.Arguments = string(b.ToolCall.Args)
				if len(b.ToolCall.Args) == 0 {
					tc.Function.Arguments = "{}"
				}
				w.ToolCalls = append(w.ToolCalls, tc)
			case b.Thinking != nil:
				if len(b.Thinking.Raw) > 0 {
					details = append(details, b.Thinking.Raw)
				}
			default:
				text.WriteString(b.Text)
			}
		}
		if text.Len() > 0 {
			w.Content = text.String()
		}
		w.ReasoningDetails = joinArrays(details)
		return w
	default:
		var parts []textPart
		for _, b := range m.Blocks {
			if b.Thinking == nil && b.ToolCall == nil {
				parts = append(parts, textPart{Type: "text", Text: b.Text})
			}
		}
		if len(parts) == 1 {
			return wireMessage{Role: "user", Content: parts[0].Text}
		}
		return wireMessage{Role: "user", Content: parts}
	}
}

// joinArrays concatenates JSON arrays; a single array is returned byte-for-byte.
func joinArrays(arrays []json.RawMessage) json.RawMessage {
	switch len(arrays) {
	case 0:
		return nil
	case 1:
		return arrays[0]
	}
	var all []json.RawMessage
	for _, a := range arrays {
		var items []json.RawMessage
		if json.Unmarshal(a, &items) == nil {
			all = append(all, items...)
		}
	}
	out, _ := json.Marshal(all)
	return out
}

// Stream chunk shapes.

type wireChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string           `json:"content"`
			Reasoning        string           `json:"reasoning"`
			ReasoningDetails []map[string]any `json:"reasoning_details"`
			ToolCalls        []wireToolCall   `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *wireError `json:"error"`
}

type wireUsage struct {
	PromptTokens        int      `json:"prompt_tokens"`
	CompletionTokens    int      `json:"completion_tokens"`
	CacheCreationTokens int      `json:"cache_creation_input_tokens"`
	Cost                *float64 `json:"cost"`
	SurchargeCost       float64  `json:"surcharge_cost"`
	PromptDetails       struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type wireError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

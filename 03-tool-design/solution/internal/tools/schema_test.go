package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"harness/internal/tools"
)

type sampleArgs struct {
	Path   string   `json:"path" desc:"File path"`
	Limit  int      `json:"limit,omitempty" desc:"Max lines"`
	Mode   string   `json:"mode,omitempty" enum:"files,content"`
	Tags   []string `json:"tags,omitempty"`
	Strict bool     `json:"strict,omitempty"`
	Nested struct {
		Depth float64 `json:"depth"`
	} `json:"nested,omitempty"`
}

func TestSchemaFor(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal(tools.SchemaFor[sampleArgs](), &s); err != nil {
		t.Fatal(err)
	}
	if s["type"] != "object" || s["additionalProperties"] != false {
		t.Errorf("top level = %v", s)
	}
	props := s["properties"].(map[string]any)
	check := func(name, typ string) map[string]any {
		p, ok := props[name].(map[string]any)
		if !ok || p["type"] != typ {
			t.Errorf("property %s = %v, want type %s", name, props[name], typ)
			return map[string]any{}
		}
		return p
	}
	if check("path", "string")["description"] != "File path" {
		t.Error("desc tag not used as description")
	}
	check("limit", "integer")
	check("strict", "boolean")
	if items := check("tags", "array")["items"].(map[string]any); items["type"] != "string" {
		t.Errorf("tags items = %v", items)
	}
	if enum := check("mode", "string")["enum"].([]any); len(enum) != 2 || enum[0] != "files" {
		t.Errorf("enum = %v", enum)
	}
	nested := check("nested", "object")["properties"].(map[string]any)
	if nested["depth"].(map[string]any)["type"] != "number" {
		t.Errorf("nested = %v", nested)
	}
	req := s["required"].([]any)
	if len(req) != 1 || req[0] != "path" {
		t.Errorf("required = %v; fields without omitempty are required", req)
	}
}

func TestDecode(t *testing.T) {
	got, err := tools.Decode[sampleArgs](json.RawMessage(`{"path":"a.go","limit":5}`))
	if err != nil || got.Path != "a.go" || got.Limit != 5 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDecodeErrorsAreActionable(t *testing.T) {
	cases := []struct {
		args string
		want []string
	}{
		{`{"limit":5}`, []string{`missing required field "path"`}},
		{`{"path":"a","colour":"red"}`, []string{`unknown field "colour"`, "path", "limit"}},
		{`{"path":"a","limit":"ten"}`, []string{`field "limit"`, "integer"}},
		{`{"path":`, []string{"not valid JSON"}},
		{`["a"]`, []string{"JSON object"}},
	}
	for _, c := range cases {
		_, err := tools.Decode[sampleArgs](json.RawMessage(c.args))
		if err == nil {
			t.Errorf("%s: want error", c.args)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error %q should mention %q", c.args, err, w)
			}
		}
		if !strings.HasPrefix(err.Error(), "invalid arguments") {
			t.Errorf("%s: error %q should start with \"invalid arguments\"", c.args, err)
		}
	}
}

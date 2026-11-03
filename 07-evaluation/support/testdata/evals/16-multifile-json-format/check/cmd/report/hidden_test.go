package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestHiddenFormats(t *testing.T) {
	var text, def bytes.Buffer
	if err := run([]string{"--format", "text"}, &text); err != nil {
		t.Fatal(err)
	}
	run(nil, &def)
	want := "go     3\nrust   2\nzig    1\n"
	if text.String() != want || def.String() != want {
		t.Errorf("text output changed: %q / %q", text.String(), def.String())
	}
	var js bytes.Buffer
	if err := run([]string{"--format", "json"}, &js); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(js.Bytes(), &rows); err != nil {
		t.Fatalf("not a JSON array: %q", js.String())
	}
	if len(rows) != 3 || rows[0]["name"] != "go" || rows[0]["count"] != 3.0 || rows[0]["share"] != 0.5 || rows[2]["share"] != 0.17 {
		t.Errorf("json = %v", rows)
	}
	if err := run([]string{"--format", "xml"}, &js); err == nil {
		t.Error("unknown format should be an error")
	}
}

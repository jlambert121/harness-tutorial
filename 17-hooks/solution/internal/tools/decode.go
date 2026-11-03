package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Decode parses tool arguments into T, producing errors written for the model:
// each names the problem field and what was expected, so the next call can fix it.
func Decode[T any](args json.RawMessage) (T, error) {
	var out T
	fail := func(format string, a ...any) (T, error) {
		return out, fmt.Errorf("invalid arguments: "+format, a...)
	}
	args = bytes.TrimSpace(args)
	if len(args) == 0 {
		args = []byte("{}")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) || !json.Valid(args) {
			return fail("not valid JSON (%v). Send a JSON object matching the tool's schema.", err)
		}
		return fail("expected a JSON object, got %s.", kindOf(args))
	}

	t := reflect.TypeOf(out)
	fields := map[string]reflect.StructField{}
	var names, required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, omit, ok := jsonName(f)
		if !ok {
			continue
		}
		fields[name] = f
		names = append(names, name)
		if !omit {
			required = append(required, name)
		}
	}
	for k := range obj {
		if _, ok := fields[k]; !ok {
			return fail("unknown field %q; valid fields are: %s.", k, strings.Join(names, ", "))
		}
	}
	for _, r := range required {
		if v, ok := obj[r]; !ok || string(v) == "null" {
			return fail("missing required field %q.", r)
		}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		ptr := reflect.New(fields[k].Type)
		if err := json.Unmarshal(obj[k], ptr.Interface()); err != nil {
			return fail("field %q must be %s, got %s.", k, schemaOf(fields[k].Type, false)["type"], kindOf(obj[k]))
		}
	}
	if err := json.Unmarshal(args, &out); err != nil {
		return fail("%v", err)
	}
	return out, nil
}

func kindOf(v json.RawMessage) string {
	v = bytes.TrimSpace(v)
	if len(v) == 0 {
		return "nothing"
	}
	switch v[0] {
	case '"':
		return "string " + string(v)
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	return "number " + string(v)
}

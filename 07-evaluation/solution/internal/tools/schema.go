package tools

import (
	"encoding/json"
	"reflect"
	"strings"
)

// SchemaFor derives a JSON Schema from a struct's fields. Field names come
// from `json` tags; fields without omitempty are required. Tags: `desc:"..."`
// sets the description, `enum:"a,b"` restricts string values.
func SchemaFor[T any]() json.RawMessage {
	var zero T
	b, _ := json.Marshal(schemaOf(reflect.TypeOf(zero), true))
	return b
}

func schemaOf(t reflect.Type, top bool) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), false)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), false)}
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, omit, ok := jsonName(f)
			if !ok {
				continue
			}
			p := schemaOf(f.Type, false)
			if d := f.Tag.Get("desc"); d != "" {
				p["description"] = d
			}
			if e := f.Tag.Get("enum"); e != "" {
				p["enum"] = strings.Split(e, ",")
			}
			props[name] = p
			if !omit {
				required = append(required, name)
			}
		}
		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return map[string]any{}
}

func jsonName(f reflect.StructField) (name string, omitempty, ok bool) {
	if !f.IsExported() {
		return "", false, false
	}
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, p := range parts[1:] {
		if p == "omitempty" || p == "omitzero" {
			omitempty = true
		}
	}
	return name, omitempty, true
}

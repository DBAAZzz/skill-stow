package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
)

// Keep member order, including placeholder order, because it defines generated signatures.
type member struct {
	name  string
	value json.RawMessage
}
type object []member

func decodeObject(data []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	result := object{}
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := token.(string)
		if seen[key] {
			return nil, fmt.Errorf("duplicate JSON key %q", key)
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		result = append(result, member{key, value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON object")
	}
	return result, nil
}
func (o object) get(key string) (json.RawMessage, bool) {
	for _, field := range o {
		if field.name == key {
			return field.value, true
		}
	}
	return nil, false
}
func rawJSON(value any) json.RawMessage {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	// All callers supply strings or JSON-serializable metadata.
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	return bytes.TrimSpace(buf.Bytes())
}
func (o object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(rawJSON(field.name))
		buf.WriteByte(':')
		buf.Write(field.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
func (o object) encoded() ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(o); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func loadCatalog(path, locale string) (object, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return object{{"@@locale", rawJSON(locale)}}, nil
	}
	if err != nil {
		return nil, err
	}
	obj, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return obj, nil
}
func catalogPlaceholders(catalog object, key string) ([]placeholder, error) {
	result := []placeholder{}
	raw, ok := catalog.get("@" + key)
	if !ok {
		return result, nil
	}
	metadata, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	raw, ok = metadata.get("placeholders")
	if !ok {
		return result, nil
	}
	fields, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		var spec struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(field.value, &spec); err != nil {
			return nil, err
		}
		// ARB's default placeholder type is String.
		if spec.Type == "" {
			spec.Type = "String"
		}
		result = append(result, placeholder{field.name, spec.Type})
	}
	return result, nil
}
func metadataFor(item entry) object {
	description := item.Description
	if description == "" {
		description = fmt.Sprintf("%s:%d", item.File, item.Line)
	}
	fields := object{}
	for _, p := range item.Placeholders {
		fields = append(fields, member{p.Name, rawJSON(object{{"type", rawJSON(p.Type)}})})
	}
	return object{{"description", rawJSON(description)}, {"placeholders", rawJSON(fields)}}
}
func mergeMessage(catalog *object, item entry, text string, source bool) (bool, error) {
	raw, exists := catalog.get(item.Key)
	if exists {
		var previous string
		if err := json.Unmarshal(raw, &previous); err != nil || previous != text {
			return false, fmt.Errorf("catalog key %q has conflicting text", item.Key)
		}
		_, hasMetadata := catalog.get("@" + item.Key)
		if source || hasMetadata {
			previousPH, err := catalogPlaceholders(*catalog, item.Key)
			if err != nil {
				return false, fmt.Errorf("catalog key %q: %w", item.Key, err)
			}
			if !reflect.DeepEqual(previousPH, item.Placeholders) {
				return false, fmt.Errorf("catalog key %q has conflicting placeholders (including order)", item.Key)
			}
		}
		return false, nil
	}
	if _, exists := catalog.get("@" + item.Key); exists {
		return false, fmt.Errorf("catalog key %q has orphan metadata", item.Key)
	}
	*catalog = append(*catalog, member{item.Key, rawJSON(text)})
	if source {
		*catalog = append(*catalog, member{"@" + item.Key, rawJSON(metadataFor(item))})
	}
	return true, nil
}

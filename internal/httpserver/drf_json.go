package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type drfFieldValidation struct {
	field   string
	message string
}

// decodeDRFJSONObject applies DRF's scalar conversion before unmarshalling
// into the typed request. CharField accepts JSON numbers, but rejects booleans.
func decodeDRFJSONObject(body io.Reader, target any, chars, choices, lists []string) (*drfFieldValidation, error) {
	var root json.RawMessage
	if err := json.NewDecoder(body).Decode(&root); err != nil {
		return nil, err
	}
	value := bytes.TrimSpace(root)
	if bytes.Equal(value, []byte("null")) {
		return &drfFieldValidation{"non_field_errors", "No data provided"}, nil
	}
	if len(value) == 0 || value[0] != '{' {
		kind := "str"
		if len(value) != 0 {
			switch value[0] {
			case '[':
				kind = "list"
			case 't', 'f':
				kind = "bool"
			case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				kind = "int"
				if bytes.ContainsAny(value, ".eE") {
					kind = "float"
				}
			}
		}
		return &drfFieldValidation{"non_field_errors", "Invalid data. Expected a dictionary, but got " + kind + "."}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(root, &fields); err != nil {
		return nil, err
	}
	for _, field := range chars {
		raw, ok := fields[field]
		if !ok {
			continue
		}
		value, message := drfStringValue(raw)
		if message != "" {
			return &drfFieldValidation{field, message}, nil
		}
		fields[field] = value
	}
	for _, field := range choices {
		raw, ok := fields[field]
		if !ok {
			continue
		}
		value := bytes.TrimSpace(raw)
		if bytes.Equal(value, []byte("null")) {
			return &drfFieldValidation{field, "This field may not be null."}, nil
		}
		if len(value) == 0 || value[0] != '"' {
			var choice any
			if err := json.Unmarshal(value, &choice); err != nil {
				return nil, err
			}
			label := fmt.Sprint(choice)
			if label == "true" {
				label = "True"
			} else if label == "false" {
				label = "False"
			}
			return &drfFieldValidation{field, fmt.Sprintf("%q is not a valid choice.", label)}, nil
		}
	}
	for _, field := range lists {
		raw, ok := fields[field]
		if !ok {
			continue
		}
		value := bytes.TrimSpace(raw)
		if bytes.Equal(value, []byte("null")) {
			return &drfFieldValidation{field, "This field may not be null."}, nil
		}
		if len(value) == 0 || value[0] != '[' {
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal(value, &items); err != nil {
			return nil, err
		}
		for i, item := range items {
			converted, message := drfStringValue(item)
			if message != "" {
				return &drfFieldValidation{field, message}, nil
			}
			items[i] = converted
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			return nil, err
		}
		fields[field] = encoded
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return nil, json.Unmarshal(encoded, target)
}

func drfStringValue(raw json.RawMessage) (json.RawMessage, string) {
	value := bytes.TrimSpace(raw)
	if bytes.Equal(value, []byte("null")) {
		return nil, "This field may not be null."
	}
	if len(value) != 0 && value[0] == '"' {
		return value, ""
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil && number != "" {
		text := number.String()
		if strings.ContainsAny(text, ".eE") {
			if floating, err := strconv.ParseFloat(text, 64); err == nil {
				text = strconv.FormatFloat(floating, 'g', -1, 64)
				if !strings.ContainsAny(text, ".eE") {
					text += ".0"
				}
			}
		}
		encoded, err := json.Marshal(text)
		if err == nil {
			return encoded, ""
		}
	}
	return nil, "Not a valid string."
}

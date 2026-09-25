package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type drfFieldValidation struct {
	field   string
	message string
}

type drfJSONParseError string

func (e drfJSONParseError) Error() string { return string(e) }

func drfJSONErrorDetail(err error) string {
	var parseError drfJSONParseError
	if errors.As(err, &parseError) {
		return parseError.Error()
	}
	return "JSON parse error."
}

// decodeDRFJSONObject applies DRF's scalar conversion before unmarshalling
// into the typed request. CharField accepts JSON numbers, but rejects booleans.
func decodeDRFJSONObject(body io.Reader, target any, chars, choices, lists []string) (*drfFieldValidation, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	var root json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, makeDRFJSONParseError(payload, err)
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

func makeDRFJSONParseError(payload []byte, err error) error {
	position := len(payload)
	reason := "Expecting value"
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) > 0 {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) && syntax.Offset > 0 {
			position = min(int(syntax.Offset)-1, len(payload))
		}
		if strings.Contains(err.Error(), "after top-level value") {
			decoder := json.NewDecoder(bytes.NewReader(payload))
			var first json.RawMessage
			if decoder.Decode(&first) == nil {
				position = int(decoder.InputOffset())
				for position < len(payload) && (payload[position] == ' ' || payload[position] == '\n' || payload[position] == '\r' || payload[position] == '\t') {
					position++
				}
			}
			reason = "Extra data"
		} else if comma := bytes.LastIndexByte(payload, ','); comma >= 0 {
			after := bytes.TrimSpace(payload[comma+1:])
			if bytes.Equal(after, []byte("}")) || bytes.Equal(after, []byte("]")) {
				position = comma
				reason = "Illegal trailing comma before end of object"
				if after[0] == ']' {
					reason = "Illegal trailing comma before end of array"
				}
			}
		}
		if reason == "Expecting value" && strings.Contains(err.Error(), "unexpected end of JSON input") {
			position = len(payload)
			if trimmed[len(trimmed)-1] == '{' {
				reason = "Expecting property name enclosed in double quotes"
			}
		} else if reason == "Expecting value" && position > 0 {
			before := bytes.TrimSpace(payload[:position])
			if len(before) > 0 && (before[len(before)-1] == '{' || before[len(before)-1] == ',') {
				reason = "Expecting property name enclosed in double quotes"
			}
		}
	}
	position = min(max(position, 0), len(payload))
	prefix := payload[:position]
	line := bytes.Count(prefix, []byte{'\n'}) + 1
	lastNewline := bytes.LastIndexByte(prefix, '\n')
	column := utf8.RuneCount(prefix[lastNewline+1:]) + 1
	character := utf8.RuneCount(prefix)
	return drfJSONParseError(fmt.Sprintf("JSON parse error - %s: line %d column %d (char %d)", reason, line, column, character))
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

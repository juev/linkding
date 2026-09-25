package httpserver

import (
	"encoding/json"
	"strings"
)

const adminAdditionMessage = `[{"added": {}}]`

// adminChangeMessage follows Django's json.dumps formatting for form changes.
func adminChangeMessage(fields []string) string {
	if len(fields) == 0 {
		return "[]"
	}
	return `[{"changed": {"fields": ` + adminJSONList(fields) + `}}]`
}

func adminJSONList(fields []string) string {
	var message strings.Builder
	message.WriteByte('[')
	for index, field := range fields {
		if index > 0 {
			message.WriteString(", ")
		}
		encoded, _ := json.Marshal(field)
		message.Write(encoded)
	}
	message.WriteByte(']')
	return message.String()
}

package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type drfAPIMetadata struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Renders     []string        `json:"renders"`
	Parses      []string        `json:"parses"`
	Actions     json.RawMessage `json:"actions,omitempty"`
}

// writeAPIMetadata returns the SimpleMetadata shape used by the pinned DRF API.
// method and schema are empty for routes that do not expose a writable serializer.
func writeAPIMetadata(w http.ResponseWriter, name, method, schema string) {
	metadata := drfAPIMetadata{
		Name:        name,
		Description: "",
		Renders:     []string{"application/json", "text/html"},
		Parses:      []string{"application/json", "application/x-www-form-urlencoded", "multipart/form-data"},
	}
	if method != "" {
		var err error
		metadata.Actions, err = json.Marshal(map[string]json.RawMessage{method: json.RawMessage(schema)})
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "Server error")
			return
		}
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, "Server error")
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

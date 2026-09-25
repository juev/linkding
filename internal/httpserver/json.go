package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// writeJSON matches DRF's compact JSON output without a trailing newline.
func writeJSON(w http.ResponseWriter, value any) error {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	_, err := w.Write(bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}))
	return err
}

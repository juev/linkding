package httpserver

import (
	"log"
	"net/http"
)

// logServerError keeps the cause in console logs without exposing it in responses.
func logServerError(r *http.Request, err error) {
	log.Printf("request error method=%q path=%q error=%q", r.Method, safeRequestPath(r.URL.Path), err)
}

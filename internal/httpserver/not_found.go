package httpserver

import "net/http"

const notFoundHTML = `
<!doctype html>
<html lang="en">
<head>
  <title>Not Found</title>
</head>
<body>
  <h1>Not Found</h1><p>The requested resource was not found on this server.</p>
</body>
</html>
`

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "Accept-Language, Cookie")
	w.Header().Set("Content-Language", selectedAdminLanguage(r).Code)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Content-Length", "179")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(notFoundHTML))
	}
}

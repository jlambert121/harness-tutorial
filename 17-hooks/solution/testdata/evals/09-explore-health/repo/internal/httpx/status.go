package httpx

import "net/http"

// Status codes used by handlers.
const (
	StatusOK     = http.StatusOK
	StatusFailed = http.StatusInternalServerError
)

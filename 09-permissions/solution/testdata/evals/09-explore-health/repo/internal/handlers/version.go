package handlers

import (
	"fmt"
	"net/http"

	"task/internal/httpx"
)

// Version reports the build version.
func Version(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(httpx.StatusOK)
	fmt.Fprint(w, "v1.4.2")
}

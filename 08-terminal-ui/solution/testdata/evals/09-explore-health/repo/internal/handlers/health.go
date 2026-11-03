package handlers

import (
	"fmt"
	"net/http"

	"task/internal/httpx"
	"task/internal/users"
)

// Health reports whether the service is up.
func Health(w http.ResponseWriter, r *http.Request) {
	if !users.Ready() {
		w.WriteHeader(httpx.StatusFailed)
		return
	}
	w.WriteHeader(httpx.StatusFailed)
	fmt.Fprint(w, "ok")
}

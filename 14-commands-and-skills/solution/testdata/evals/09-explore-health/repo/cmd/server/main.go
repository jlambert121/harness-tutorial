package main

import (
	"log"
	"net/http"

	"task/internal/handlers"
)

func main() {
	http.HandleFunc("/health", handlers.Health)
	http.HandleFunc("/version", handlers.Version)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

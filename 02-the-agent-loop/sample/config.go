package main

import (
	"encoding/json"
	"os"
)

type Config struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Verbose bool   `json:"verbose"`
}

// LoadConfig reads JSON config from path, falling back to defaults.go when path is empty.
func LoadConfig(path string) (Config, error) {
	if path == "" {
		return Defaults(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := Defaults()
	err = json.Unmarshal(b, &cfg)
	return cfg, err
}

// Package config provides minimal environment/.env handling shared by the CLIs.
package config

import (
	"log"
	"os"
	"strings"
)

// LoadDotEnv loads KEY=value pairs from a .env file if present. It does not
// override variables already set in the real environment, and a missing file is
// not an error.
func LoadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}

// MustEnv returns the value of key, or exits if it is unset/empty.
func MustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s (see .env.example)", key)
	}
	return v
}

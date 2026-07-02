package main

import "os"

type config struct {
	DatabaseURL string
	ListenAddr  string
}

func loadConfig() config {
	return config{
		DatabaseURL: envOr("DATABASE_URL", ""),
		ListenAddr:  envOr("LISTEN_ADDR", ":8080"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

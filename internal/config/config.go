// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"strings"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	Env         string
	AdminToken  string
	AutoMigrate bool
}

func Load() Config {
	return Config{
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://durable:durable@localhost:5432/durable?sslmode=disable"),
		Env:         getenv("ENV", "dev"),
		AdminToken:  getenv("ADMIN_TOKEN", ""),
		AutoMigrate: getenvBool("AUTO_MIGRATE", true),
	}
}

func getenv(key, defaultValue string) string {
	v := os.Getenv(key)
	if v != "" {
		return v
	}
	return defaultValue
}

func getenvBool(key string, defaultValue bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return defaultValue
	}

	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

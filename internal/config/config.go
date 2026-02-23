// SPDX-License-Identifier: Apache-2.0

package config

import "os"

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	Env         string
	AdminToken  string
}

func Load() Config {
	return Config{
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://durable:durable@localhost:5432/durable?sslmode=disable"),
		Env:         getenv("ENV", "dev"),
		AdminToken:  getenv("ADMIN_TOKEN", ""),
	}
}

func getenv(key, defaultValue string) string {
	v := os.Getenv(key)
	if v != "" {
		return v
	}
	return defaultValue
}

// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a project-standard slog logger.
// - env=dev: text handler with source locations
// - env=prod: JSON handler without source locations
// LOG_LEVEL controls the level (debug/info/warn/error), default info.
func NewLogger(env string) *slog.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))

	if strings.EqualFold(strings.TrimSpace(env), "prod") {
		return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     level,
			AddSource: false,
		}))
	}

	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
	}))
}

func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

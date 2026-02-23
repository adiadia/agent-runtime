// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{in: "", want: slog.LevelInfo},
		{in: "debug", want: slog.LevelDebug},
		{in: "info", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "unknown", want: slog.LevelInfo},
	}

	for _, tc := range cases {
		if got := parseLevel(tc.in); got != tc.want {
			t.Fatalf("parseLevel(%q): expected %v got %v", tc.in, tc.want, got)
		}
	}
}

func TestNewLogger(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	if logger := NewLogger("dev"); logger == nil {
		t.Fatal("expected dev logger")
	}
	if logger := NewLogger("prod"); logger == nil {
		t.Fatal("expected prod logger")
	}
}

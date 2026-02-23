// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	logger := newLogger()

	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	ctx := context.Background()

	switch os.Args[1] {
	case "validate":
		if err := runValidate(ctx, logger); err != nil {
			logger.Error("validation failed", "error", err)
			os.Exit(1)
		}
		logger.Info("validation passed")
	default:
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func runValidate(ctx context.Context, logger *slog.Logger) error {
	started := time.Now()

	if err := runGofmtCheck(ctx, logger); err != nil {
		return err
	}

	if err := runCommand(ctx, logger, "go vet", "go", "vet", "./..."); err != nil {
		return err
	}

	if err := runCommand(ctx, logger, "go test unit", "go", "test", "./..."); err != nil {
		return err
	}

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		logger.Info("skipping integration tests", "reason", "DATABASE_URL is not set")
	} else {
		if err := runCommand(
			ctx,
			logger,
			"go test integration",
			"go",
			"test",
			"-count=1",
			"-tags=integration",
			"./internal/repository",
			"./internal/worker",
		); err != nil {
			return err
		}
	}

	logger.Info("validation complete", "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func runGofmtCheck(ctx context.Context, logger *slog.Logger) error {
	files, err := listGoFiles(".")
	if err != nil {
		return fmt.Errorf("list go files: %w", err)
	}

	if len(files) == 0 {
		logger.Info("skipping gofmt check", "reason", "no go files found")
		return nil
	}

	logger.Info("running step", "step", "gofmt check", "files", len(files))
	started := time.Now()

	args := make([]string, 0, len(files)+1)
	args = append(args, "-l")
	args = append(args, files...)

	cmd := exec.CommandContext(ctx, "gofmt", args...)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gofmt check failed: %w", err)
	}

	unformatted := strings.TrimSpace(string(out))
	if unformatted != "" {
		return fmt.Errorf("gofmt would change files:\n%s", unformatted)
	}

	logger.Info("step completed", "step", "gofmt check", "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func runCommand(ctx context.Context, logger *slog.Logger, step string, name string, args ...string) error {
	logger.Info("running step", "step", step, "command", strings.Join(append([]string{name}, args...), " "))
	started := time.Now()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	err := cmd.Run()
	duration := time.Since(started)
	if err != nil {
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		logger.Error("step failed", "step", step, "duration_ms", duration.Milliseconds(), "exit_code", exitCode)
		return err
	}

	logger.Info("step completed", "step", step, "duration_ms", duration.Milliseconds())
	return nil
}

func listGoFiles(root string) ([]string, error) {
	files := make([]string, 0, 64)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			switch name {
			case ".git", ".cache", ".gocache", ".gomodcache", "vendor":
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(os.Getenv("LOG_LEVEL")),
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

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, "usage: go run ./cmd/cli validate")
}

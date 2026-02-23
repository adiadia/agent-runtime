// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"

	"github.com/adiadia/agent-runtime/internal/auth"
	"github.com/adiadia/agent-runtime/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APIKeyRepository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewAPIKeyRepository(pool *pgxpool.Pool, logger *slog.Logger) *APIKeyRepository {
	if logger == nil {
		logger = slog.Default()
	}

	return &APIKeyRepository{
		pool:   pool,
		logger: logger,
	}
}

func (r *APIKeyRepository) ResolveAPIKey(ctx context.Context, bearerToken string) (auth.APIKey, bool, error) {
	if bearerToken == "" {
		return auth.APIKey{}, false, nil
	}
	tokenHash := sha256Hex(bearerToken)

	var key auth.APIKey
	err := r.pool.QueryRow(ctx,
		`SELECT id, max_concurrent_runs, max_requests_per_min
		 FROM api_keys
		 WHERE token_hash=$1 AND revoked_at IS NULL`,
		tokenHash,
	).Scan(&key.ID, &key.MaxConcurrentRuns, &key.MaxRequestsPerMin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.APIKey{}, false, nil
		}
		r.logger.Error("resolve api key failed", "error", err)
		return auth.APIKey{}, false, err
	}

	if key.MaxConcurrentRuns <= 0 {
		key.MaxConcurrentRuns = domain.DefaultMaxConcurrentRuns
	}
	if key.MaxRequestsPerMin <= 0 {
		key.MaxRequestsPerMin = domain.DefaultMaxRequestsPerMin
	}

	return key, true, nil
}

func (r *APIKeyRepository) CreateAPIKey(ctx context.Context, params domain.CreateAPIKeyParams) (domain.CreatedAPIKey, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return domain.CreatedAPIKey{}, domain.ErrInvalidAPIKeyName
	}

	maxConcurrentRuns := params.MaxConcurrentRuns
	if maxConcurrentRuns <= 0 {
		maxConcurrentRuns = domain.DefaultMaxConcurrentRuns
	}
	maxRequestsPerMin := params.MaxRequestsPerMin
	if maxRequestsPerMin <= 0 {
		maxRequestsPerMin = domain.DefaultMaxRequestsPerMin
	}

	token, tokenHash, err := generateAPIKeyToken()
	if err != nil {
		r.logger.Error("generate api key token failed", "error", err)
		return domain.CreatedAPIKey{}, err
	}

	apiKeyID := uuid.New()
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO api_keys (id, name, token_hash, max_concurrent_runs, max_requests_per_min)
		VALUES ($1, $2, $3, $4, $5)
	`,
		apiKeyID,
		name,
		tokenHash,
		maxConcurrentRuns,
		maxRequestsPerMin,
	); err != nil {
		r.logger.Error("create api key failed", "name", name, "error", err)
		return domain.CreatedAPIKey{}, err
	}

	return domain.CreatedAPIKey{
		ID:    apiKeyID,
		Token: token,
	}, nil
}

func (r *APIKeyRepository) ListAPIKeys(ctx context.Context) ([]domain.APIKeyRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, max_concurrent_runs, max_requests_per_min, created_at
		FROM api_keys
		WHERE revoked_at IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		r.logger.Error("list api keys query failed", "error", err)
		return nil, err
	}
	defer rows.Close()

	keys := make([]domain.APIKeyRecord, 0, 32)
	for rows.Next() {
		var record domain.APIKeyRecord
		if err := rows.Scan(
			&record.ID,
			&record.Name,
			&record.MaxConcurrentRuns,
			&record.MaxRequestsPerMin,
			&record.CreatedAt,
		); err != nil {
			return nil, err
		}
		keys = append(keys, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func (r *APIKeyRepository) RevokeAPIKey(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE api_keys
		SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		r.logger.Error("revoke api key failed", "api_key_id", id, "error", err)
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func generateAPIKeyToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := "sk_live_" + hex.EncodeToString(raw)
	return token, sha256Hex(token), nil
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

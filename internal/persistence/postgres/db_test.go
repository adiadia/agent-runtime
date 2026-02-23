// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"
)

func TestNewPoolInvalidURL(t *testing.T) {
	t.Parallel()

	pool, err := NewPool(context.Background(), "://not-valid")
	if err == nil {
		t.Fatal("expected invalid URL to return an error")
	}
	if pool != nil {
		t.Fatal("expected pool to be nil on parse error")
	}
}

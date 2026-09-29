package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStoreSettlementRejectsInvalidClaimBeforeDatabaseUse(t *testing.T) {
	t.Parallel()

	store := &Store{pool: &pgxpool.Pool{}}
	methods := []struct {
		name string
		run  func(ClaimedEvent) error
	}{
		{
			name: "mark published",
			run: func(event ClaimedEvent) error {
				return store.MarkPublished(context.Background(), event)
			},
		},
		{
			name: "retry",
			run: func(event ClaimedEvent) error {
				return store.Retry(context.Background(), event, time.Second)
			},
		},
		{
			name: "mark failed",
			run: func(event ClaimedEvent) error {
				return store.MarkFailed(context.Background(), event)
			},
		},
	}

	invalidClaims := []struct {
		name  string
		event ClaimedEvent
	}{
		{
			name:  "non-positive row ID",
			event: ClaimedEvent{ID: 0, LockToken: "claim-token"},
		},
		{
			name:  "missing lock token",
			event: ClaimedEvent{ID: 1},
		},
	}

	for _, method := range methods {
		method := method
		t.Run(method.name, func(t *testing.T) {
			t.Parallel()
			for _, tt := range invalidClaims {
				tt := tt
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					if err := method.run(tt.event); err == nil {
						t.Fatal("settlement error = nil, want invalid claim error")
					}
				})
			}
		})
	}
}

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/platform/db"
)

// PostgresRepository implements Repository against *pgxpool.Pool. Every query runs through
// db.WithTx so the transaction-scoped tenant search_path is applied (jobs live in the tenant's
// schema, not `public`); the multi-statement claim/fail flows keep their SELECT ... FOR UPDATE
// SKIP LOCKED and UPDATE in the same transaction via the callback.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, jobType string, payload map[string]any) (*Job, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("jobs: encoding payload: %w", err)
	}

	job := &Job{Type: jobType, Payload: payload, Status: StatusPending}
	err = db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO jobs (type, payload) VALUES ($1, $2)
			RETURNING id, retries, max_retries, created_at
		`, jobType, encoded).Scan(&job.ID, &job.Retries, &job.MaxRetries, &job.CreatedAt)
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: creating: %w", err)
	}
	return job, nil
}

func (r *PostgresRepository) FindByID(ctx context.Context, id uuid.UUID) (*Job, error) {
	var job Job
	var payload, result []byte
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, type, payload, status, retries, max_retries, result, locked_until, created_at
			FROM jobs WHERE id = $1
		`, id).Scan(&job.ID, &job.Type, &payload, &job.Status, &job.Retries, &job.MaxRetries, &result, &job.LockedUntil, &job.CreatedAt)
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("jobs: finding by id: %w", err)
	}
	if err := json.Unmarshal(payload, &job.Payload); err != nil {
		return nil, fmt.Errorf("jobs: decoding payload: %w", err)
	}
	if result != nil {
		if err := json.Unmarshal(result, &job.Result); err != nil {
			return nil, fmt.Errorf("jobs: decoding result: %w", err)
		}
	}
	return &job, nil
}

func (r *PostgresRepository) ClaimNext(ctx context.Context, leaseDuration time.Duration) (*Job, error) {
	var claimed *Job
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var job Job
		var payload, result []byte
		err := tx.QueryRow(ctx, `
			SELECT id, type, payload, status, retries, max_retries, result, created_at
			FROM jobs
			WHERE status = 'PENDING' OR (status = 'PROCESSING' AND locked_until < now())
			ORDER BY created_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		`).Scan(&job.ID, &job.Type, &payload, &job.Status, &job.Retries, &job.MaxRetries, &result, &job.CreatedAt)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil
			}
			return fmt.Errorf("jobs: selecting next job: %w", err)
		}
		if err := json.Unmarshal(payload, &job.Payload); err != nil {
			return fmt.Errorf("jobs: decoding payload: %w", err)
		}

		lockedUntil := time.Now().Add(leaseDuration)
		if _, err := tx.Exec(ctx, `
			UPDATE jobs SET status = 'PROCESSING', locked_until = $2
			WHERE id = $1
		`, job.ID, lockedUntil); err != nil {
			return fmt.Errorf("jobs: claiming job: %w", err)
		}
		job.Status = StatusProcessing
		job.LockedUntil = &lockedUntil
		claimed = &job
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *PostgresRepository) MarkCompleted(ctx context.Context, jobID uuid.UUID, result map[string]any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("jobs: encoding result: %w", err)
	}
	if err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE jobs SET status = 'COMPLETED', result = $2, locked_until = NULL
			WHERE id = $1
		`, jobID, encoded)
		return err
	}); err != nil {
		return fmt.Errorf("jobs: marking completed: %w", err)
	}
	return nil
}

func (r *PostgresRepository) MarkFailed(ctx context.Context, jobID uuid.UUID, causeMessage string) error {
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var retries, maxRetries int
		if err := tx.QueryRow(ctx, `SELECT retries, max_retries FROM jobs WHERE id = $1`, jobID).Scan(&retries, &maxRetries); err != nil {
			return fmt.Errorf("jobs: reading retry count: %w", err)
		}

		retries++
		nextStatus := StatusPending
		var result []byte
		if retries >= maxRetries {
			nextStatus = StatusFailed
			encoded, err := json.Marshal(map[string]any{"error": causeMessage})
			if err != nil {
				return fmt.Errorf("jobs: encoding failure result: %w", err)
			}
			result = encoded
		}

		if _, err := tx.Exec(ctx, `
			UPDATE jobs SET status = $2, retries = $3, result = COALESCE($4, result), locked_until = NULL
			WHERE id = $1
		`, jobID, nextStatus, retries, result); err != nil {
			return fmt.Errorf("jobs: recording failure: %w", err)
		}
		return nil
	})
	return err
}

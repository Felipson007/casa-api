// Package postgres implements notifications.Repository — the only file in the notifications
// module that imports a SQL driver. Every query goes through db.WithTx.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atlas/atlas-go/internal/notifications"
	"github.com/atlas/atlas-go/internal/platform/db"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

var _ notifications.Repository = (*Repository)(nil)

func (r *Repository) List(ctx context.Context, readOnly *bool) ([]notifications.Notification, error) {
	var result []notifications.Notification
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, type, title, message, link, read, created_at
			FROM notifications
			WHERE ($1::boolean IS NULL OR read = $1)
			  AND type <> 'STEAM_ACHIEVEMENT_UNLOCKED'
			ORDER BY created_at DESC
		`, readOnly)
		if err != nil {
			return fmt.Errorf("querying notifications: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			n, err := scanNotification(rows)
			if err != nil {
				return err
			}
			result = append(result, n)
		}
		return rows.Err()
	})
	return result, err
}

func (r *Repository) FindByID(ctx context.Context, id uuid.UUID) (*notifications.Notification, error) {
	var result *notifications.Notification
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, type, title, message, link, read, created_at
			FROM notifications
			WHERE id = $1
		`, id)
		n, err := scanNotification(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &n
		return nil
	})
	return result, err
}

func (r *Repository) FindEquivalent(ctx context.Context, notificationType notifications.NotificationType, title, message string, link *string) (*notifications.Notification, error) {
	var result *notifications.Notification
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, type, title, message, link, read, created_at
			FROM notifications
			WHERE type = $1 AND title = $2 AND message = $3 AND link IS NOT DISTINCT FROM $4
			ORDER BY created_at DESC
			LIMIT 1
		`, notificationType, title, message, link)
		n, err := scanNotification(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &n
		return nil
	})
	return result, err
}

func (r *Repository) Create(ctx context.Context, n notifications.Notification) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO notifications (id, type, title, message, link, read)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, n.ID, n.Type, n.Title, n.Message, n.Link, n.Read)
		if err != nil {
			return fmt.Errorf("inserting notification: %w", err)
		}
		return nil
	})
}

func (r *Repository) MarkRead(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE notifications SET read = true WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("marking notification read: %w", err)
		}
		return nil
	})
}

func (r *Repository) MarkAllRead(ctx context.Context) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE notifications SET read = true WHERE read = false`)
		if err != nil {
			return fmt.Errorf("marking all notifications read: %w", err)
		}
		return nil
	})
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM notifications WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("deleting notification: %w", err)
		}
		return nil
	})
}

func (r *Repository) DeleteAll(ctx context.Context) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM notifications`); err != nil {
			return fmt.Errorf("deleting all notifications: %w", err)
		}
		return nil
	})
}

func (r *Repository) GetConfiguration(ctx context.Context) (*notifications.Configuration, error) {
	var result *notifications.Configuration
	err := db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, retention_days, created_at
			FROM notification_configuration
			ORDER BY created_at
			LIMIT 1
		`)
		var c notifications.Configuration
		err := row.Scan(&c.ID, &c.RetentionDays, &c.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		result = &c
		return nil
	})
	return result, err
}

func (r *Repository) CreateConfiguration(ctx context.Context, c notifications.Configuration) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO notification_configuration (id, retention_days)
			VALUES ($1, $2)
		`, c.ID, c.RetentionDays)
		if err != nil {
			return fmt.Errorf("inserting notification configuration: %w", err)
		}
		return nil
	})
}

func (r *Repository) UpdateConfiguration(ctx context.Context, c notifications.Configuration) error {
	return db.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE notification_configuration SET retention_days = $2 WHERE id = $1
		`, c.ID, c.RetentionDays)
		if err != nil {
			return fmt.Errorf("updating notification configuration: %w", err)
		}
		return nil
	})
}

// rowScanner is satisfied by both pgx.Rows and pgx.Row.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(row rowScanner) (notifications.Notification, error) {
	var n notifications.Notification
	err := row.Scan(&n.ID, &n.Type, &n.Title, &n.Message, &n.Link, &n.Read, &n.CreatedAt)
	return n, err
}

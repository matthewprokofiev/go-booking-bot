package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

func rollback(ctx context.Context, tx pgx.Tx, log *slog.Logger) {
	// После успешного Commit откат вернёт ErrTxClosed — это норма, а не проблема.
	if err := tx.Rollback(ctx); err != nil && !isTxClosed(err) {
		log.Error("откат транзакции", "error", err)
	}
}

func isTxClosed(err error) bool {
	return errors.Is(err, pgx.ErrTxClosed)
}

// SyncAdmins приводит таблицу admins к списку из ADMIN_TG_IDS: ENV — источник правды,
// таблица нужна для читаемых имён и джойнов.
func (s *Storage) SyncAdmins(ctx context.Context, tgIDs []int64) error {
	if len(tgIDs) == 0 {
		if _, err := s.pool.Exec(ctx, `DELETE FROM admins`); err != nil {
			return fmt.Errorf("очистка списка админов: %w", err)
		}
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("старт транзакции синка админов: %w", err)
	}
	defer rollback(ctx, tx, s.log)

	for _, id := range tgIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO admins (tg_id, name)
			VALUES ($1, '')
			ON CONFLICT (tg_id) DO NOTHING`, id); err != nil {
			return fmt.Errorf("добавление админа %d: %w", id, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM admins WHERE tg_id <> ALL($1)`, tgIDs); err != nil {
		return fmt.Errorf("удаление лишних админов: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("коммит синка админов: %w", err)
	}
	return nil
}

func (s *Storage) AdminIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT tg_id FROM admins ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("выборка админов: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("чтение админа: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("выборка админов: %w", err)
	}
	return ids, nil
}

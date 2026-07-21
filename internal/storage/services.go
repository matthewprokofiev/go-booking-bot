package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

func (s *Storage) ActiveServices(ctx context.Context) ([]domain.Service, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, duration_min, price, is_active
		FROM services
		WHERE is_active = TRUE
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("выборка услуг: %w", err)
	}
	defer rows.Close()

	var services []domain.Service
	for rows.Next() {
		var svc domain.Service
		if err := rows.Scan(&svc.ID, &svc.Name, &svc.DurationMin, &svc.Price, &svc.IsActive); err != nil {
			return nil, fmt.Errorf("чтение услуги: %w", err)
		}
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("выборка услуг: %w", err)
	}
	return services, nil
}

func (s *Storage) ServiceByID(ctx context.Context, id int64) (domain.Service, error) {
	var svc domain.Service
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, duration_min, price, is_active
		FROM services
		WHERE id = $1`, id).
		Scan(&svc.ID, &svc.Name, &svc.DurationMin, &svc.Price, &svc.IsActive)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Service{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Service{}, fmt.Errorf("выборка услуги %d: %w", id, err)
	}
	return svc, nil
}

func (s *Storage) CountServices(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM services`).Scan(&n); err != nil {
		return 0, fmt.Errorf("подсчёт услуг: %w", err)
	}
	return n, nil
}

package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

// FreeSlotsForDay отдаёт свободные слоты услуги за сутки. Границы дня приходят уже
// посчитанными в таймзоне бизнеса (domain.DayBounds) — репозиторий их не изобретает.
// notBefore отсекает уже начавшиеся слоты: для сегодняшнего дня это «сейчас», для
// будущих дней — раньше начала суток, поэтому ни на что не влияет. Фильтр «в прошлом»
// живёт здесь, а не в хендлере, чтобы счётчик свободных слотов (DaysWithFreeSlots)
// и фактический список совпадали.
func (s *Storage) FreeSlotsForDay(ctx context.Context, serviceID int64, dayStart, dayEnd, notBefore time.Time) ([]domain.Slot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, service_id, slot_start, slot_end, is_booked
		FROM schedule_slots
		WHERE service_id = $1
		  AND is_booked = FALSE
		  AND slot_start >= $2
		  AND slot_start < $3
		  AND slot_start > $4
		ORDER BY slot_start`, serviceID, dayStart, dayEnd, notBefore)
	if err != nil {
		return nil, fmt.Errorf("выборка свободных слотов: %w", err)
	}
	defer rows.Close()

	return scanSlots(rows)
}

// DaysWithFreeSlots нужен, чтобы не показывать клиенту дни, в которых записаться некуда.
// notBefore отсекает прошедшие слоты той же границей, что и FreeSlotsForDay, — иначе
// счётчик на кнопке дня («17.07 (5)») разошёлся бы с реальным списком после клика.
func (s *Storage) DaysWithFreeSlots(ctx context.Context, serviceID int64, from, to, notBefore time.Time, loc *time.Location) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT slot_start
		FROM schedule_slots
		WHERE service_id = $1
		  AND is_booked = FALSE
		  AND slot_start >= $2
		  AND slot_start < $3
		  AND slot_start > $4`, serviceID, from, to, notBefore)
	if err != nil {
		return nil, fmt.Errorf("выборка дней со свободными слотами: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var start time.Time
		if err := rows.Scan(&start); err != nil {
			return nil, fmt.Errorf("чтение слота: %w", err)
		}
		counts[domain.FormatDay(start, loc)]++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("выборка дней со свободными слотами: %w", err)
	}
	return counts, nil
}

func (s *Storage) SlotByID(ctx context.Context, id int64) (domain.Slot, error) {
	var slot domain.Slot
	err := s.pool.QueryRow(ctx, `
		SELECT id, service_id, slot_start, slot_end, is_booked
		FROM schedule_slots
		WHERE id = $1`, id).
		Scan(&slot.ID, &slot.ServiceID, &slot.Start, &slot.End, &slot.IsBooked)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Slot{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Slot{}, fmt.Errorf("выборка слота %d: %w", id, err)
	}
	return slot, nil
}

func scanSlots(rows pgx.Rows) ([]domain.Slot, error) {
	var slots []domain.Slot
	for rows.Next() {
		var slot domain.Slot
		if err := rows.Scan(&slot.ID, &slot.ServiceID, &slot.Start, &slot.End, &slot.IsBooked); err != nil {
			return nil, fmt.Errorf("чтение слота: %w", err)
		}
		slots = append(slots, slot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перебор слотов: %w", err)
	}
	return slots, nil
}

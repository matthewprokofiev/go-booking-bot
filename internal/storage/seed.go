package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

const seedDays = 7

var demoServices = []domain.Service{
	{Name: "Мужская стрижка", DurationMin: 60, Price: 1500},
	{Name: "Женская стрижка", DurationMin: 90, Price: 2500},
	{Name: "Окрашивание", DurationMin: 120, Price: 4000},
	{Name: "Укладка", DurationMin: 30, Price: 1000},
}

// Seed наполняет демо-данными пустую базу. Сид программный, а не goose-миграцией,
// потому что статичный SQL не видит BUSINESS_TZ и WORK_HOURS_*: слоты нужно
// раскладывать по рабочим часам конкретной таймзоны, а не по хардкоду в UTC.
// Идемпотентность держится на проверке «в services пусто» — на существующую базу сид не лезет.
func (s *Storage) Seed(ctx context.Context, loc *time.Location, workStart, workEnd int, now time.Time) error {
	count, err := s.CountServices(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		s.log.Debug("сид пропущен: услуги уже есть", "services", count)
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("старт транзакции сида: %w", err)
	}
	defer rollback(ctx, tx, s.log)

	var slotsCreated int
	for _, svc := range demoServices {
		var serviceID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO services (name, duration_min, price, is_active)
			VALUES ($1, $2, $3, TRUE)
			RETURNING id`, svc.Name, svc.DurationMin, svc.Price).Scan(&serviceID)
		if err != nil {
			return fmt.Errorf("создание услуги %q: %w", svc.Name, err)
		}

		for _, day := range domain.UpcomingDays(now, loc, seedDays) {
			for _, start := range domain.GenerateSlotStarts(day, loc, workStart, workEnd, svc.DurationMin) {
				// Слоты в прошлом бессмысленны: в первый день сидим только будущее.
				if start.Before(now) {
					continue
				}
				end := start.Add(time.Duration(svc.DurationMin) * time.Minute)

				if _, err := tx.Exec(ctx, `
					INSERT INTO schedule_slots (service_id, slot_start, slot_end)
					VALUES ($1, $2, $3)
					ON CONFLICT (service_id, slot_start) DO NOTHING`, serviceID, start, end); err != nil {
					return fmt.Errorf("создание слота для %q: %w", svc.Name, err)
				}
				slotsCreated++
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("коммит сида: %w", err)
	}

	s.log.Info("демо-данные засеяны",
		"services", len(demoServices), "slots", slotsCreated,
		"business_tz", loc.String(), "work_hours", fmt.Sprintf("%d-%d", workStart, workEnd))
	return nil
}

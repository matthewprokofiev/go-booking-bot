package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

const bookingSelect = `
	SELECT b.id, b.slot_id, b.client_tg_id, b.client_name, b.status, b.created_at,
	       s.name, s.duration_min, s.price,
	       sl.slot_start, sl.slot_end
	FROM bookings b
	JOIN schedule_slots sl ON sl.id = b.slot_id
	JOIN services s ON s.id = sl.service_id`

// CreateBooking бронирует слот в одной транзакции.
//
// Защита от двойной брони держится на атомарном UPDATE ... WHERE is_booked = FALSE:
// проверка занятости и захват слота происходят в одном выражении, поэтому два
// параллельных клиента не могут оба увидеть слот свободным. Проигравшая транзакция
// получит RowsAffected = 0 и ошибку ErrSlotTaken — гонка не превращается в двойную запись.
// Дополнительно страхует частичный уникальный индекс uniq_active_booking.
func (s *Storage) CreateBooking(ctx context.Context, slotID, clientTgID int64, clientName string) (domain.Booking, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("старт транзакции брони: %w", err)
	}
	defer rollback(ctx, tx, s.log)

	tag, err := tx.Exec(ctx, `
		UPDATE schedule_slots
		SET is_booked = TRUE
		WHERE id = $1 AND is_booked = FALSE`, slotID)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("захват слота %d: %w", slotID, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Booking{}, domain.ErrSlotTaken
	}

	var bookingID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO bookings (slot_id, client_tg_id, client_name, status)
		VALUES ($1, $2, $3, 'active')
		RETURNING id`, slotID, clientTgID, clientName).Scan(&bookingID)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("создание брони на слот %d: %w", slotID, err)
	}

	booking, err := bookingByIDTx(ctx, tx, bookingID)
	if err != nil {
		return domain.Booking{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Booking{}, fmt.Errorf("коммит транзакции брони: %w", err)
	}
	return booking, nil
}

// CancelBooking снимает бронь и освобождает слот атомарно: иначе можно получить
// отменённую бронь на «занятом» навсегда слоте (или наоборот — свободный слот с активной бронью).
// Строка брони не удаляется, а получает status='cancelled' — история отмен остаётся,
// а частичный индекс uniq_active_booking разрешает новую активную бронь на этот слот.
//
// byTgID == 0 означает отмену админом: проверка владельца пропускается.
func (s *Storage) CancelBooking(ctx context.Context, bookingID, byTgID int64) (domain.Booking, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Booking{}, fmt.Errorf("старт транзакции отмены: %w", err)
	}
	defer rollback(ctx, tx, s.log)

	var slotID, ownerTgID int64
	var status string
	err = tx.QueryRow(ctx, `
		SELECT slot_id, client_tg_id, status
		FROM bookings
		WHERE id = $1
		FOR UPDATE`, bookingID).Scan(&slotID, &ownerTgID, &status)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Booking{}, fmt.Errorf("выборка брони %d: %w", bookingID, err)
	}

	if byTgID != 0 && ownerTgID != byTgID {
		return domain.Booking{}, domain.ErrNotOwner
	}
	if status != domain.StatusActive {
		return domain.Booking{}, domain.ErrNotCancelable
	}

	if _, err := tx.Exec(ctx, `
		UPDATE bookings SET status = 'cancelled' WHERE id = $1`, bookingID); err != nil {
		return domain.Booking{}, fmt.Errorf("отмена брони %d: %w", bookingID, err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE schedule_slots SET is_booked = FALSE WHERE id = $1`, slotID); err != nil {
		return domain.Booking{}, fmt.Errorf("освобождение слота %d: %w", slotID, err)
	}

	booking, err := bookingByIDTx(ctx, tx, bookingID)
	if err != nil {
		return domain.Booking{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Booking{}, fmt.Errorf("коммит транзакции отмены: %w", err)
	}
	return booking, nil
}

// ActiveBookingsByClient фильтрует по slot_end, а не slot_start: запись, слот которой
// уже начался, но ещё не закончился, остаётся активной и держит слот — клиент должен
// видеть её в «Мои записи» и иметь возможность отменить.
func (s *Storage) ActiveBookingsByClient(ctx context.Context, clientTgID int64, now time.Time) ([]domain.Booking, error) {
	rows, err := s.pool.Query(ctx, bookingSelect+`
		WHERE b.client_tg_id = $1
		  AND b.status = 'active'
		  AND sl.slot_end >= $2
		ORDER BY sl.slot_start`, clientTgID, now)
	if err != nil {
		return nil, fmt.Errorf("выборка записей клиента: %w", err)
	}
	defer rows.Close()

	return scanBookings(rows)
}

func (s *Storage) ActiveBookingsForDay(ctx context.Context, dayStart, dayEnd time.Time) ([]domain.Booking, error) {
	rows, err := s.pool.Query(ctx, bookingSelect+`
		WHERE b.status = 'active'
		  AND sl.slot_start >= $1
		  AND sl.slot_start < $2
		ORDER BY sl.slot_start`, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("выборка записей на день: %w", err)
	}
	defer rows.Close()

	return scanBookings(rows)
}

func (s *Storage) BookingByID(ctx context.Context, id int64) (domain.Booking, error) {
	row := s.pool.QueryRow(ctx, bookingSelect+` WHERE b.id = $1`, id)
	return scanBooking(row)
}

func bookingByIDTx(ctx context.Context, tx pgx.Tx, id int64) (domain.Booking, error) {
	row := tx.QueryRow(ctx, bookingSelect+` WHERE b.id = $1`, id)
	return scanBooking(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanBooking(row scannable) (domain.Booking, error) {
	var b domain.Booking
	err := row.Scan(&b.ID, &b.SlotID, &b.ClientTgID, &b.ClientName, &b.Status, &b.CreatedAt,
		&b.ServiceName, &b.DurationMin, &b.Price, &b.SlotStart, &b.SlotEnd)

	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Booking{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Booking{}, fmt.Errorf("чтение брони: %w", err)
	}
	return b, nil
}

func scanBookings(rows pgx.Rows) ([]domain.Booking, error) {
	var bookings []domain.Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перебор броней: %w", err)
	}
	return bookings, nil
}

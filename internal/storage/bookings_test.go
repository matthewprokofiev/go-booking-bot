package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

func newMockStorage(t *testing.T) (*Storage, pgxmock.PgxPoolIface) {
	t.Helper()

	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("создание pgxmock: %v", err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("не все ожидания выполнены: %v", err)
		}
		mock.Close()
	})

	return NewWithPool(mock, slog.New(slog.NewTextHandler(io.Discard, nil))), mock
}

func bookingRows(slotStart time.Time) *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"id", "slot_id", "client_tg_id", "client_name", "status", "created_at",
		"name", "duration_min", "price", "slot_start", "slot_end",
	}).AddRow(
		int64(1), int64(10), int64(555), "Иван", domain.StatusActive, slotStart,
		"Стрижка", 60, 1500, slotStart, slotStart.Add(time.Hour),
	)
}

func TestCreateBookingCommitsOnSuccess(t *testing.T) {
	st, mock := newMockStorage(t)
	slotStart := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE schedule_slots").
		WithArgs(int64(10)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("INSERT INTO bookings").
		WithArgs(int64(10), int64(555), "Иван").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT b.id").
		WithArgs(int64(1)).
		WillReturnRows(bookingRows(slotStart))
	mock.ExpectCommit()
	mock.ExpectRollback() // defer rollback после Commit — ожидаемый no-op

	got, err := st.CreateBooking(context.Background(), 10, 555, "Иван")
	if err != nil {
		t.Fatalf("CreateBooking: неожиданная ошибка: %v", err)
	}
	if got.ID != 1 || got.SlotID != 10 || got.Status != domain.StatusActive {
		t.Errorf("бронь = %+v, ожидалась активная бронь #1 на слот 10", got)
	}
}

// Занятый слот: атомарный UPDATE не задел ни одной строки. Ровно так проявляется
// проигрыш гонки за слот — бронь создаваться не должна, транзакция откатывается.
func TestCreateBookingOnTakenSlotReturnsErrSlotTaken(t *testing.T) {
	st, mock := newMockStorage(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE schedule_slots").
		WithArgs(int64(10)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	// Ни INSERT, ни Commit не ожидаются: ExpectationsWereMet поймает лишний запрос.
	mock.ExpectRollback()

	_, err := st.CreateBooking(context.Background(), 10, 555, "Иван")

	if !errors.Is(err, domain.ErrSlotTaken) {
		t.Fatalf("CreateBooking на занятом слоте = %v, ожидалась ErrSlotTaken", err)
	}
}

func TestCreateBookingRollsBackOnInsertFailure(t *testing.T) {
	st, mock := newMockStorage(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE schedule_slots").
		WithArgs(int64(10)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("INSERT INTO bookings").
		WithArgs(int64(10), int64(555), "Иван").
		WillReturnError(errors.New("нарушение uniq_active_booking"))
	// Откат обязателен: иначе слот остался бы помечен занятым без брони.
	mock.ExpectRollback()

	if _, err := st.CreateBooking(context.Background(), 10, 555, "Иван"); err == nil {
		t.Fatal("CreateBooking: ожидалась ошибка вставки")
	}
}

func TestCancelBookingCommitsOnSuccess(t *testing.T) {
	st, mock := newMockStorage(t)
	slotStart := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id, client_tg_id, status").
		WithArgs(int64(1)).
		WillReturnRows(pgxmock.NewRows([]string{"slot_id", "client_tg_id", "status"}).
			AddRow(int64(10), int64(555), domain.StatusActive))
	mock.ExpectExec("UPDATE bookings SET status = 'cancelled'").
		WithArgs(int64(1)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	// Освобождение слота в той же транзакции — иначе слот «занят навсегда».
	mock.ExpectExec("UPDATE schedule_slots SET is_booked = FALSE").
		WithArgs(int64(10)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SELECT b.id").
		WithArgs(int64(1)).
		WillReturnRows(bookingRows(slotStart))
	mock.ExpectCommit()
	mock.ExpectRollback()

	if _, err := st.CancelBooking(context.Background(), 1, 555); err != nil {
		t.Fatalf("CancelBooking: неожиданная ошибка: %v", err)
	}
}

func TestCancelBookingByNonOwnerIsRejected(t *testing.T) {
	st, mock := newMockStorage(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id, client_tg_id, status").
		WithArgs(int64(1)).
		WillReturnRows(pgxmock.NewRows([]string{"slot_id", "client_tg_id", "status"}).
			AddRow(int64(10), int64(555), domain.StatusActive))
	mock.ExpectRollback()

	_, err := st.CancelBooking(context.Background(), 1, 999)

	if !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("CancelBooking чужой брони = %v, ожидалась ErrNotOwner", err)
	}
}

// byTgID = 0 — отмена админом: проверка владельца не применяется.
func TestCancelBookingByAdminSkipsOwnerCheck(t *testing.T) {
	st, mock := newMockStorage(t)
	slotStart := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id, client_tg_id, status").
		WithArgs(int64(1)).
		WillReturnRows(pgxmock.NewRows([]string{"slot_id", "client_tg_id", "status"}).
			AddRow(int64(10), int64(555), domain.StatusActive))
	mock.ExpectExec("UPDATE bookings SET status = 'cancelled'").
		WithArgs(int64(1)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE schedule_slots SET is_booked = FALSE").
		WithArgs(int64(10)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectQuery("SELECT b.id").
		WithArgs(int64(1)).
		WillReturnRows(bookingRows(slotStart))
	mock.ExpectCommit()
	mock.ExpectRollback()

	if _, err := st.CancelBooking(context.Background(), 1, 0); err != nil {
		t.Fatalf("CancelBooking админом: неожиданная ошибка: %v", err)
	}
}

func TestCancelAlreadyCancelledBooking(t *testing.T) {
	st, mock := newMockStorage(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id, client_tg_id, status").
		WithArgs(int64(1)).
		WillReturnRows(pgxmock.NewRows([]string{"slot_id", "client_tg_id", "status"}).
			AddRow(int64(10), int64(555), domain.StatusCancelled))
	mock.ExpectRollback()

	_, err := st.CancelBooking(context.Background(), 1, 555)

	if !errors.Is(err, domain.ErrNotCancelable) {
		t.Fatalf("повторная отмена = %v, ожидалась ErrNotCancelable", err)
	}
}

func TestCancelMissingBooking(t *testing.T) {
	st, mock := newMockStorage(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT slot_id, client_tg_id, status").
		WithArgs(int64(404)).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := st.CancelBooking(context.Background(), 404, 555)

	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("отмена несуществующей брони = %v, ожидалась ErrNotFound", err)
	}
}

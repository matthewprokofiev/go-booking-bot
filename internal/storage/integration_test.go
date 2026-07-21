//go:build integration

// Интеграционные тесты на живом Postgres в testcontainers. Вынесены под тег,
// потому что требуют Docker: `make test` должен оставаться быстрым и работать без него.
// Запуск: make test-integration
package storage

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

func setupPostgres(t *testing.T) *Storage {
	t.Helper()

	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("booking"),
		postgres.WithUsername("booking"),
		postgres.WithPassword("booking"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("запуск контейнера postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("остановка контейнера: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("строка подключения: %v", err)
	}

	if err := Migrate(ctx, dsn); err != nil {
		t.Fatalf("миграции: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("создание пула: %v", err)
	}
	t.Cleanup(pool.Close)

	return NewWithPool(pool, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

func seedOneSlot(t *testing.T, st *Storage) (serviceID, slotID int64) {
	t.Helper()
	ctx := context.Background()

	err := st.pool.QueryRow(ctx, `
		INSERT INTO services (name, duration_min, price, is_active)
		VALUES ('Стрижка', 60, 1500, TRUE) RETURNING id`).Scan(&serviceID)
	if err != nil {
		t.Fatalf("создание услуги: %v", err)
	}

	start := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	err = st.pool.QueryRow(ctx, `
		INSERT INTO schedule_slots (service_id, slot_start, slot_end)
		VALUES ($1, $2, $3) RETURNING id`, serviceID, start, start.Add(time.Hour)).Scan(&slotID)
	if err != nil {
		t.Fatalf("создание слота: %v", err)
	}
	return serviceID, slotID
}

// Главный инвариант: сколько бы клиентов ни ломились в один слот одновременно,
// активная бронь получится ровно одна.
func TestNoDoubleBookingUnderConcurrency(t *testing.T) {
	st := setupPostgres(t)
	_, slotID := seedOneSlot(t, st)

	const clients = 20

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		taken     int
		otherErrs []error
	)

	start := make(chan struct{})
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(clientID int64) {
			defer wg.Done()
			<-start // стартуем все разом, чтобы гонка была настоящей

			_, err := st.CreateBooking(context.Background(), slotID, clientID, "Клиент")

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrSlotTaken):
				taken++
			default:
				otherErrs = append(otherErrs, err)
			}
		}(int64(1000 + i))
	}
	close(start)
	wg.Wait()

	if len(otherErrs) > 0 {
		t.Fatalf("неожиданные ошибки: %v", otherErrs)
	}
	if succeeded != 1 {
		t.Errorf("успешных броней = %d, ожидалась ровно 1", succeeded)
	}
	if taken != clients-1 {
		t.Errorf("отказов ErrSlotTaken = %d, ожидалось %d", taken, clients-1)
	}

	assertActiveBookings(t, st, slotID, 1)
	assertSlotBooked(t, st, slotID, true)
}

// Сценарий из плана: клиент отменил → слот снова свободен → его занял другой клиент.
// Здесь же проверяется, что частичный индекс uniq_active_booking допускает
// несколько отменённых броней на одном слоте.
func TestCancelledSlotCanBeRebooked(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()
	_, slotID := seedOneSlot(t, st)

	first, err := st.CreateBooking(ctx, slotID, 111, "Первый клиент")
	if err != nil {
		t.Fatalf("первая бронь: %v", err)
	}

	// Пока бронь активна, слот занят для всех остальных.
	if _, err := st.CreateBooking(ctx, slotID, 222, "Второй клиент"); !errors.Is(err, domain.ErrSlotTaken) {
		t.Fatalf("бронь занятого слота = %v, ожидалась ErrSlotTaken", err)
	}

	cancelled, err := st.CancelBooking(ctx, first.ID, 111)
	if err != nil {
		t.Fatalf("отмена первой брони: %v", err)
	}
	if cancelled.Status != domain.StatusCancelled {
		t.Errorf("статус после отмены = %q, ожидалось %q", cancelled.Status, domain.StatusCancelled)
	}
	assertSlotBooked(t, st, slotID, false)

	second, err := st.CreateBooking(ctx, slotID, 222, "Второй клиент")
	if err != nil {
		t.Fatalf("перебронирование освободившегося слота: %v", err)
	}
	if second.ClientTgID != 222 {
		t.Errorf("владелец новой брони = %d, ожидалось 222", second.ClientTgID)
	}

	// Отменённая и новая активная бронь сосуществуют на одном слоте —
	// колоночный UNIQUE(slot_id) такое запретил бы.
	assertActiveBookings(t, st, slotID, 1)
	assertTotalBookings(t, st, slotID, 2)
}

// Отменить можно много раз подряд — частичный индекс считает только активные.
func TestManyCancelledBookingsOnSameSlot(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()
	_, slotID := seedOneSlot(t, st)

	const rounds = 5
	for i := 0; i < rounds; i++ {
		b, err := st.CreateBooking(ctx, slotID, int64(100+i), "Клиент")
		if err != nil {
			t.Fatalf("бронь на круге %d: %v", i, err)
		}
		if _, err := st.CancelBooking(ctx, b.ID, int64(100+i)); err != nil {
			t.Fatalf("отмена на круге %d: %v", i, err)
		}
	}

	assertActiveBookings(t, st, slotID, 0)
	assertTotalBookings(t, st, slotID, rounds)
	assertSlotBooked(t, st, slotID, false)
}

func TestCancelIsRejectedForNonOwner(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()
	_, slotID := seedOneSlot(t, st)

	booking, err := st.CreateBooking(ctx, slotID, 111, "Владелец")
	if err != nil {
		t.Fatalf("бронь: %v", err)
	}

	if _, err := st.CancelBooking(ctx, booking.ID, 999); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("отмена чужой брони = %v, ожидалась ErrNotOwner", err)
	}
	// Отказ не должен ничего менять.
	assertActiveBookings(t, st, slotID, 1)
	assertSlotBooked(t, st, slotID, true)

	// Админ (byTgID = 0) ту же бронь отменить вправе.
	if _, err := st.CancelBooking(ctx, booking.ID, 0); err != nil {
		t.Fatalf("отмена админом: %v", err)
	}
	assertActiveBookings(t, st, slotID, 0)
	assertSlotBooked(t, st, slotID, false)
}

// Свободные слоты на день считаются по границам дня в таймзоне бизнеса, а не в UTC.
func TestFreeSlotsForDayUsesBusinessTZBounds(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()

	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	var serviceID int64
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO services (name, duration_min, price, is_active)
		VALUES ('Стрижка', 60, 1500, TRUE) RETURNING id`).Scan(&serviceID); err != nil {
		t.Fatalf("создание услуги: %v", err)
	}

	// 22:00 UTC 17 июля = 01:00 MSK 18 июля. По UTC слот принадлежит 17-му,
	// по московскому времени — 18-му; выборка обязана следовать бизнес-таймзоне.
	lateSlot := time.Date(2026, 7, 17, 22, 0, 0, 0, time.UTC)
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO schedule_slots (service_id, slot_start, slot_end)
		VALUES ($1, $2, $3)`, serviceID, lateSlot, lateSlot.Add(time.Hour)); err != nil {
		t.Fatalf("создание слота: %v", err)
	}

	// notBefore заведомо раньше слота, чтобы проверять именно границы дня, а не фильтр прошлого.
	notBefore := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	day17, _ := domain.ParseDay("2026-07-17", msk)
	start17, end17 := domain.DayBounds(day17, msk)
	slots17, err := st.FreeSlotsForDay(ctx, serviceID, start17, end17, notBefore)
	if err != nil {
		t.Fatalf("FreeSlotsForDay 17-го: %v", err)
	}
	if len(slots17) != 0 {
		t.Errorf("на 17 июля по МСК слотов = %d, ожидалось 0", len(slots17))
	}

	day18, _ := domain.ParseDay("2026-07-18", msk)
	start18, end18 := domain.DayBounds(day18, msk)
	slots18, err := st.FreeSlotsForDay(ctx, serviceID, start18, end18, notBefore)
	if err != nil {
		t.Fatalf("FreeSlotsForDay 18-го: %v", err)
	}
	if len(slots18) != 1 {
		t.Fatalf("на 18 июля по МСК слотов = %d, ожидался 1", len(slots18))
	}
	if got := slots18[0].Start.In(msk).Hour(); got != 1 {
		t.Errorf("час слота в МСК = %d, ожидался 1", got)
	}
}

// notBefore отсекает уже начавшиеся слоты, и счётчик DaysWithFreeSlots совпадает
// с фактическим списком FreeSlotsForDay — иначе кнопка дня врала бы числом (баг из ревью).
func TestPastSlotsFilteredConsistently(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()

	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	var serviceID int64
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO services (name, duration_min, price, is_active)
		VALUES ('Стрижка', 60, 1500, TRUE) RETURNING id`).Scan(&serviceID); err != nil {
		t.Fatalf("создание услуги: %v", err)
	}

	day, _ := domain.ParseDay("2026-07-17", msk)
	dayStart, dayEnd := domain.DayBounds(day, msk)

	// Три слота: 10:00, 12:00, 14:00 МСК.
	for _, h := range []int{10, 12, 14} {
		start := dayStart.Add(time.Duration(h) * time.Hour)
		if _, err := st.pool.Exec(ctx, `
			INSERT INTO schedule_slots (service_id, slot_start, slot_end)
			VALUES ($1, $2, $3)`, serviceID, start, start.Add(time.Hour)); err != nil {
			t.Fatalf("создание слота %d: %v", h, err)
		}
	}

	// "Сейчас" — 12:30 МСК: слот 10:00 в прошлом, 12:00 начался, будущим остаётся только 14:00.
	now := dayStart.Add(12*time.Hour + 30*time.Minute)

	slots, err := st.FreeSlotsForDay(ctx, serviceID, dayStart, dayEnd, now)
	if err != nil {
		t.Fatalf("FreeSlotsForDay: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("свободных будущих слотов = %d, ожидался 1 (только 14:00)", len(slots))
	}
	if got := slots[0].Start.In(msk).Hour(); got != 14 {
		t.Errorf("оставшийся слот в %d:00 МСК, ожидался 14:00", got)
	}

	counts, err := st.DaysWithFreeSlots(ctx, serviceID, dayStart, dayEnd, now, msk)
	if err != nil {
		t.Fatalf("DaysWithFreeSlots: %v", err)
	}
	// Счётчик на кнопке дня обязан совпадать с реальным списком.
	if got := counts["2026-07-17"]; got != len(slots) {
		t.Errorf("счётчик дня = %d, фактических слотов = %d — расхождение", got, len(slots))
	}
}

// Сид не должен ничего дописывать в непустую базу.
func TestSeedIsIdempotent(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()

	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	now := time.Date(2026, 7, 17, 6, 0, 0, 0, time.UTC)

	if err := st.Seed(ctx, msk, 10, 20, now); err != nil {
		t.Fatalf("первый сид: %v", err)
	}
	first := countRows(t, st, "schedule_slots")
	if first == 0 {
		t.Fatal("первый сид не создал ни одного слота")
	}

	if err := st.Seed(ctx, msk, 10, 20, now); err != nil {
		t.Fatalf("повторный сид: %v", err)
	}
	if second := countRows(t, st, "schedule_slots"); second != first {
		t.Errorf("после повторного сида слотов = %d, ожидалось %d", second, first)
	}
}

// Сид обязан класть слоты только в рабочие часы: иначе демка предложит запись ночью.
func TestSeedRespectsWorkHoursInBusinessTZ(t *testing.T) {
	st := setupPostgres(t)
	ctx := context.Background()

	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	const workStart, workEnd = 10, 20
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)

	if err := st.Seed(ctx, msk, workStart, workEnd, now); err != nil {
		t.Fatalf("сид: %v", err)
	}

	rows, err := st.pool.Query(ctx, `SELECT slot_start, slot_end FROM schedule_slots`)
	if err != nil {
		t.Fatalf("выборка слотов: %v", err)
	}
	defer rows.Close()

	var checked int
	for rows.Next() {
		var start, end time.Time
		if err := rows.Scan(&start, &end); err != nil {
			t.Fatalf("чтение слота: %v", err)
		}
		checked++

		localStart := start.In(msk)
		if localStart.Hour() < workStart {
			t.Errorf("слот %s начинается раньше рабочего дня", localStart)
		}
		if localEnd := end.In(msk); localEnd.Hour() > workEnd ||
			(localEnd.Hour() == workEnd && localEnd.Minute() > 0) {
			t.Errorf("слот %s заканчивается позже рабочего дня", localEnd)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("перебор слотов: %v", err)
	}
	if checked == 0 {
		t.Fatal("сид не создал слотов")
	}
}

func assertActiveBookings(t *testing.T, st *Storage, slotID int64, want int) {
	t.Helper()

	var got int
	err := st.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM bookings WHERE slot_id = $1 AND status = 'active'`, slotID).Scan(&got)
	if err != nil {
		t.Fatalf("подсчёт активных броней: %v", err)
	}
	if got != want {
		t.Errorf("активных броней на слоте %d = %d, ожидалось %d", slotID, got, want)
	}
}

func assertTotalBookings(t *testing.T, st *Storage, slotID int64, want int) {
	t.Helper()

	var got int
	err := st.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM bookings WHERE slot_id = $1`, slotID).Scan(&got)
	if err != nil {
		t.Fatalf("подсчёт броней: %v", err)
	}
	if got != want {
		t.Errorf("всего броней на слоте %d = %d, ожидалось %d", slotID, got, want)
	}
}

func assertSlotBooked(t *testing.T, st *Storage, slotID int64, want bool) {
	t.Helper()

	var got bool
	err := st.pool.QueryRow(context.Background(), `
		SELECT is_booked FROM schedule_slots WHERE id = $1`, slotID).Scan(&got)
	if err != nil {
		t.Fatalf("чтение слота: %v", err)
	}
	if got != want {
		t.Errorf("is_booked слота %d = %v, ожидалось %v", slotID, got, want)
	}
}

func countRows(t *testing.T, st *Storage, table string) int {
	t.Helper()

	var n int
	if err := st.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("подсчёт строк в %s: %v", table, err)
	}
	return n
}

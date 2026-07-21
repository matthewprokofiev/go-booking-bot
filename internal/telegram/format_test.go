package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

func testLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

// Время записи показывается в таймзоне бизнеса, а не в UTC, в котором живёт контейнер.
func TestFormatTimeUsesBusinessTZ(t *testing.T) {
	msk := testLoc(t)
	utc := time.Date(2026, 7, 17, 7, 30, 0, 0, time.UTC)

	if got := formatTime(utc, msk); got != "10:30" {
		t.Errorf("formatTime = %q, ожидалось 10:30 (МСК)", got)
	}
}

func TestFormatDayHuman(t *testing.T) {
	msk := testLoc(t)

	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{name: "пятница", in: time.Date(2026, 7, 17, 12, 0, 0, 0, msk), want: "17 июля (пт)"},
		{name: "новый год", in: time.Date(2026, 1, 1, 12, 0, 0, 0, msk), want: "1 января (чт)"},
		{
			// 22:00 UTC — по Москве уже следующий день.
			name: "конвертация из UTC", in: time.Date(2026, 7, 17, 22, 0, 0, 0, time.UTC), want: "18 июля (сб)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDayHuman(tt.in, msk); got != tt.want {
				t.Errorf("formatDayHuman = %q, ожидалось %q", got, tt.want)
			}
		})
	}
}

func TestBuildDayReport(t *testing.T) {
	msk := testLoc(t)
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, msk)

	bookings := []domain.Booking{
		{ID: 1, ClientName: "Иван", ServiceName: "Стрижка",
			SlotStart: day.Add(10 * time.Hour), SlotEnd: day.Add(11 * time.Hour)},
		{ID: 2, ClientName: "Мария", ServiceName: "Окрашивание",
			SlotStart: day.Add(12 * time.Hour), SlotEnd: day.Add(14 * time.Hour)},
	}

	lines := buildDayReport(day, len(bookings), bookings, msk)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(lines[0], "17 июля") || !strings.Contains(lines[0], "всего 2") {
		t.Errorf("заголовок = %q, ожидались дата и количество записей", lines[0])
	}
	for _, want := range []string{"Иван", "Мария", "10:00—11:00", "12:00—14:00", "#1", "#2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("в отчёте нет %q:\n%s", want, joined)
		}
	}
}

// Заголовок показывает общее число записей за день, а тело — только страницу.
// Иначе пагинация /day режет лишь кнопки, но не текст (баг из ревью).
func TestBuildDayReportShowsTotalButOnlyPageItems(t *testing.T) {
	msk := testLoc(t)
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, msk)

	pageItems := []domain.Booking{
		{ID: 9, ClientName: "Пётр", ServiceName: "Стрижка",
			SlotStart: day.Add(10 * time.Hour), SlotEnd: day.Add(11 * time.Hour)},
	}

	lines := buildDayReport(day, 25, pageItems, msk)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(lines[0], "всего 25") {
		t.Errorf("заголовок = %q, ожидалось общее число 25", lines[0])
	}
	if !strings.Contains(joined, "#9") {
		t.Errorf("в теле нет записи страницы #9:\n%s", joined)
	}
	// В теле ровно одна запись (заголовок + пустая строка + 1 строка = 3).
	if len(lines) != 3 {
		t.Errorf("строк = %d, ожидалось 3 (заголовок, пустая, одна запись)", len(lines))
	}
}

// Длинный список записей на день обязан разбиваться на несколько сообщений,
// иначе Telegram отклонит ответ целиком.
func TestLongDayReportSplitsIntoMessages(t *testing.T) {
	msk := testLoc(t)
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, msk)

	bookings := make([]domain.Booking, 150)
	for i := range bookings {
		bookings[i] = domain.Booking{
			ID:          int64(i + 1),
			ClientName:  "Александра Константинопольская",
			ServiceName: "Окрашивание и укладка",
			SlotStart:   day.Add(10 * time.Hour),
			SlotEnd:     day.Add(11 * time.Hour),
		}
	}

	lines := buildDayReport(day, len(bookings), bookings, msk)
	messages := SplitMessage(lines, splitLimit)

	if len(messages) < 2 {
		t.Fatalf("сообщений = %d, длинный отчёт должен разбиваться", len(messages))
	}
	for i, m := range messages {
		if len(m) > TelegramMaxMessageLen {
			t.Errorf("сообщение %d длиной %d превышает лимит Telegram (%d)", i, len(m), TelegramMaxMessageLen)
		}
	}

	// Ни одна запись не должна потеряться при разбиении.
	joined := strings.Join(messages, "\n")
	for _, id := range []string{"#1", "#75", "#150"} {
		if !strings.Contains(joined, id) {
			t.Errorf("после разбиения потерялась запись %s", id)
		}
	}
}

func TestClientNameFallbacks(t *testing.T) {
	tests := []struct {
		name string
		user models.User
		want string
	}{
		{name: "имя и фамилия", user: models.User{FirstName: "Иван", LastName: "Петров"}, want: "Иван Петров"},
		{name: "только имя", user: models.User{FirstName: "Иван"}, want: "Иван"},
		{name: "только username", user: models.User{Username: "ivan"}, want: "@ivan"},
		{name: "совсем пусто", user: models.User{ID: 42}, want: "Клиент 42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientName(tt.user); got != tt.want {
				t.Errorf("clientName = %q, ожидалось %q", got, tt.want)
			}
		})
	}
}

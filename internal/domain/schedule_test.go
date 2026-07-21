package domain

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func TestDayBoundsUsesBusinessTZ(t *testing.T) {
	msk := mustLoad(t, "Europe/Moscow")

	tests := []struct {
		name      string
		utc       string
		wantStart string
		wantEnd   string
	}{
		{
			// 22:30 UTC — в Москве это уже следующий день, границы должны быть его.
			name:      "поздний вечер UTC = следующий день в Москве",
			utc:       "2026-07-17T22:30:00Z",
			wantStart: "2026-07-18T00:00:00+03:00",
			wantEnd:   "2026-07-19T00:00:00+03:00",
		},
		{
			name:      "полдень UTC",
			utc:       "2026-07-17T12:00:00Z",
			wantStart: "2026-07-17T00:00:00+03:00",
			wantEnd:   "2026-07-18T00:00:00+03:00",
		},
		{
			// 21:00 UTC = ровно полночь в Москве, край интервала.
			name:      "ровно полночь по Москве",
			utc:       "2026-07-17T21:00:00Z",
			wantStart: "2026-07-18T00:00:00+03:00",
			wantEnd:   "2026-07-19T00:00:00+03:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day, err := time.Parse(time.RFC3339, tt.utc)
			if err != nil {
				t.Fatalf("разбор времени: %v", err)
			}

			start, end := DayBounds(day, msk)

			if got := start.Format(time.RFC3339); got != tt.wantStart {
				t.Errorf("start = %s, ожидалось %s", got, tt.wantStart)
			}
			if got := end.Format(time.RFC3339); got != tt.wantEnd {
				t.Errorf("end = %s, ожидалось %s", got, tt.wantEnd)
			}
			if !end.After(start) {
				t.Error("конец дня должен быть строго позже начала")
			}
		})
	}
}

// В зонах с переводом часов сутки не равны 24 часам; DayBounds обязан это учитывать.
func TestDayBoundsAcrossDSTTransition(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")

	// 29 марта 2026 — переход на летнее время, сутки длятся 23 часа.
	day := time.Date(2026, 3, 29, 12, 0, 0, 0, berlin)
	start, end := DayBounds(day, berlin)

	if got := end.Sub(start); got != 23*time.Hour {
		t.Errorf("длина суток при переходе на летнее время = %v, ожидалось 23h", got)
	}
}

func TestGenerateSlotStarts(t *testing.T) {
	msk := mustLoad(t, "Europe/Moscow")
	day := time.Date(2026, 7, 17, 15, 0, 0, 0, msk)

	tests := []struct {
		name      string
		startHour int
		endHour   int
		duration  int
		wantCount int
		wantFirst string
		wantLast  string
	}{
		{
			name: "часовые слоты 10-20", startHour: 10, endHour: 20, duration: 60,
			wantCount: 10, wantFirst: "2026-07-17T10:00:00+03:00", wantLast: "2026-07-17T19:00:00+03:00",
		},
		{
			name: "получасовые слоты 10-12", startHour: 10, endHour: 12, duration: 30,
			wantCount: 4, wantFirst: "2026-07-17T10:00:00+03:00", wantLast: "2026-07-17T11:30:00+03:00",
		},
		{
			// 90-минутный слот в 10-13: третий слот кончился бы в 14:30, за пределами дня.
			name: "слот не влезающий целиком отбрасывается", startHour: 10, endHour: 13, duration: 90,
			wantCount: 2, wantFirst: "2026-07-17T10:00:00+03:00", wantLast: "2026-07-17T11:30:00+03:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateSlotStarts(day, msk, tt.startHour, tt.endHour, tt.duration)

			if len(got) != tt.wantCount {
				t.Fatalf("количество слотов = %d, ожидалось %d", len(got), tt.wantCount)
			}
			if first := got[0].Format(time.RFC3339); first != tt.wantFirst {
				t.Errorf("первый слот = %s, ожидалось %s", first, tt.wantFirst)
			}
			if last := got[len(got)-1].Format(time.RFC3339); last != tt.wantLast {
				t.Errorf("последний слот = %s, ожидалось %s", last, tt.wantLast)
			}

			dayStart, dayEnd := DayBounds(day, msk)
			for _, slot := range got {
				if slot.Before(dayStart) || !slot.Before(dayEnd) {
					t.Errorf("слот %s вышел за границы дня", slot.Format(time.RFC3339))
				}
			}
		})
	}
}

func TestGenerateSlotStartsInvalidInput(t *testing.T) {
	msk := mustLoad(t, "Europe/Moscow")
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, msk)

	tests := []struct {
		name                            string
		startHour, endHour, durationMin int
	}{
		{name: "нулевая длительность", startHour: 10, endHour: 20, durationMin: 0},
		{name: "отрицательная длительность", startHour: 10, endHour: 20, durationMin: -30},
		{name: "перевёрнутые часы", startHour: 20, endHour: 10, durationMin: 60},
		{name: "одинаковые часы", startHour: 10, endHour: 10, durationMin: 60},
		{name: "слот длиннее рабочего дня", startHour: 10, endHour: 11, durationMin: 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateSlotStarts(day, msk, tt.startHour, tt.endHour, tt.durationMin); got != nil {
				t.Errorf("ожидался пустой результат, получено %v", got)
			}
		})
	}
}

func TestParseDayInBusinessTZ(t *testing.T) {
	msk := mustLoad(t, "Europe/Moscow")

	day, err := ParseDay("2026-07-17", msk)
	if err != nil {
		t.Fatalf("ParseDay: %v", err)
	}
	if got := day.Format(time.RFC3339); got != "2026-07-17T00:00:00+03:00" {
		t.Errorf("ParseDay = %s, ожидалось 2026-07-17T00:00:00+03:00", got)
	}

	if _, err := ParseDay("17.07.2026", msk); err == nil {
		t.Error("ParseDay с неверным форматом: ожидалась ошибка")
	}
}

func TestFormatDayConvertsToBusinessTZ(t *testing.T) {
	msk := mustLoad(t, "Europe/Moscow")

	// 22:00 UTC — в Москве уже 18 июля.
	utc := time.Date(2026, 7, 17, 22, 0, 0, 0, time.UTC)

	if got := FormatDay(utc, msk); got != "2026-07-18" {
		t.Errorf("FormatDay = %s, ожидалось 2026-07-18", got)
	}
}

func TestUpcomingDays(t *testing.T) {
	msk := mustLoad(t, "Europe/Moscow")
	today := time.Date(2026, 7, 17, 23, 45, 0, 0, msk)

	days := UpcomingDays(today, msk, 7)

	if len(days) != 7 {
		t.Fatalf("количество дней = %d, ожидалось 7", len(days))
	}
	if got := FormatDay(days[0], msk); got != "2026-07-17" {
		t.Errorf("первый день = %s, ожидалось 2026-07-17", got)
	}
	if got := FormatDay(days[6], msk); got != "2026-07-23" {
		t.Errorf("последний день = %s, ожидалось 2026-07-23", got)
	}
	for _, d := range days {
		if h := d.Hour(); h != 0 {
			t.Errorf("день %s должен начинаться в полночь, получен час %d", d, h)
		}
	}
}

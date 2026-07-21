package domain

import "time"

const DayLayout = "2006-01-02"

// DayBounds возвращает полуинтервал [начало дня, начало следующего дня) в таймзоне бизнеса.
// Контейнер живёт в UTC, поэтому границы считаются именно в loc: иначе «сегодня» для клиента
// в Москве и «сегодня» для процесса в UTC — это два разных дня в вечерние часы.
// Следующий день берётся через AddDate(0,0,1), а не через +24h: в дни перевода часов
// сутки не равны 24 часам.
func DayBounds(day time.Time, loc *time.Location) (time.Time, time.Time) {
	local := day.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	return start, end
}

func ParseDay(s string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation(DayLayout, s, loc)
}

func FormatDay(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(DayLayout)
}

// GenerateSlotStarts перечисляет начала слотов внутри рабочих часов дня в таймзоне бизнеса.
// Слот попадает в выборку, только если он целиком помещается до конца рабочего дня.
func GenerateSlotStarts(day time.Time, loc *time.Location, startHour, endHour, durationMin int) []time.Time {
	if durationMin <= 0 || startHour >= endHour {
		return nil
	}

	dayStart, _ := DayBounds(day, loc)
	workStart := dayStart.Add(time.Duration(startHour) * time.Hour)
	workEnd := dayStart.Add(time.Duration(endHour) * time.Hour)
	step := time.Duration(durationMin) * time.Minute

	var starts []time.Time
	for t := workStart; !t.Add(step).After(workEnd); t = t.Add(step) {
		starts = append(starts, t)
	}
	return starts
}

// UpcomingDays возвращает n дней начиная с today в таймзоне бизнеса.
func UpcomingDays(today time.Time, loc *time.Location, n int) []time.Time {
	start, _ := DayBounds(today, loc)

	days := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		days = append(days, start.AddDate(0, 0, i))
	}
	return days
}

package telegram

import (
	"fmt"
	"time"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

var weekdaysRU = map[time.Weekday]string{
	time.Monday:    "пн",
	time.Tuesday:   "вт",
	time.Wednesday: "ср",
	time.Thursday:  "чт",
	time.Friday:    "пт",
	time.Saturday:  "сб",
	time.Sunday:    "вс",
}

var monthsRU = map[time.Month]string{
	time.January: "января", time.February: "февраля", time.March: "марта",
	time.April: "апреля", time.May: "мая", time.June: "июня",
	time.July: "июля", time.August: "августа", time.September: "сентября",
	time.October: "октября", time.November: "ноября", time.December: "декабря",
}

// Все функции ниже принимают loc и переводят время в таймзону бизнеса:
// в БД и в процессе время живёт в UTC, показывать его пользователю в UTC нельзя.

func formatTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("15:04")
}

func formatDayHuman(t time.Time, loc *time.Location) string {
	local := t.In(loc)
	return fmt.Sprintf("%d %s (%s)", local.Day(), monthsRU[local.Month()], weekdaysRU[local.Weekday()])
}

func formatDayShort(t time.Time, loc *time.Location) string {
	local := t.In(loc)
	return fmt.Sprintf("%d.%02d %s", local.Day(), int(local.Month()), weekdaysRU[local.Weekday()])
}

func formatService(svc domain.Service) string {
	return fmt.Sprintf("%s — %d мин, %d ₽", svc.Name, svc.DurationMin, svc.Price)
}

func formatBookingLine(b domain.Booking, loc *time.Location) string {
	return fmt.Sprintf("%s—%s · %s · %s",
		formatTime(b.SlotStart, loc), formatTime(b.SlotEnd, loc), b.ServiceName, b.ClientName)
}

func formatBookingConfirmation(b domain.Booking, loc *time.Location) string {
	return fmt.Sprintf("✅ Вы записаны!\n\nУслуга: %s\nДата: %s\nВремя: %s—%s\nСтоимость: %d ₽\n\nНомер записи: #%d",
		b.ServiceName, formatDayHuman(b.SlotStart, loc),
		formatTime(b.SlotStart, loc), formatTime(b.SlotEnd, loc), b.Price, b.ID)
}

func formatAdminNotification(b domain.Booking, loc *time.Location) string {
	return fmt.Sprintf("🔔 Новая запись\n\nУслуга: %s\nДата: %s\nВремя: %s—%s\nКлиент: %s (id %d)\nЗапись: #%d",
		b.ServiceName, formatDayHuman(b.SlotStart, loc),
		formatTime(b.SlotStart, loc), formatTime(b.SlotEnd, loc), b.ClientName, b.ClientTgID, b.ID)
}

// buildDayReport собирает строки отчёта админа за день. В заголовке — общее число
// записей за день (total), в теле — только записи текущей страницы (pageBookings):
// пагинация должна резать и текст, а не только кнопки отмены. Разбиение длинного
// текста на сообщения делает SplitMessage — здесь только строки.
func buildDayReport(day time.Time, total int, pageBookings []domain.Booking, loc *time.Location) []string {
	header := fmt.Sprintf("📅 Записи на %s — всего %d", formatDayHuman(day, loc), total)
	lines := make([]string, 0, len(pageBookings)+2)
	lines = append(lines, header, "")

	for i, b := range pageBookings {
		lines = append(lines, fmt.Sprintf("%d. %s · #%d", i+1, formatBookingLine(b, loc), b.ID))
	}
	return lines
}

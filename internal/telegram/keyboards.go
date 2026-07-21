package telegram

import (
	"strconv"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

const (
	btnBook       = "📝 Записаться"
	btnMyBookings = "📋 Мои записи"
	btnAdminDay   = "📅 Записи на день"

	slotsPerRow = 3
	daysPerRow  = 2
)

func mainMenuKeyboard(isAdmin bool) *models.ReplyKeyboardMarkup {
	rows := [][]models.KeyboardButton{
		{{Text: btnBook}, {Text: btnMyBookings}},
	}
	if isAdmin {
		rows = append(rows, []models.KeyboardButton{{Text: btnAdminDay}})
	}

	return &models.ReplyKeyboardMarkup{
		Keyboard:       rows,
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
}

func servicesKeyboard(services []domain.Service) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(services))
	for _, svc := range services {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         formatService(svc),
			CallbackData: NewCallback(ActionService, strconv.FormatInt(svc.ID, 10)),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// daysKeyboard показывает только дни, где есть свободные слоты: кнопка,
// ведущая в пустой список, — гарантированное разочарование в демке.
func daysKeyboard(serviceID int64, days []time.Time, freeByDay map[string]int, loc *time.Location) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton

	for _, day := range days {
		key := domain.FormatDay(day, loc)
		free := freeByDay[key]
		if free == 0 {
			continue
		}

		row = append(row, models.InlineKeyboardButton{
			Text:         formatDayShort(day, loc) + " (" + strconv.Itoa(free) + ")",
			CallbackData: NewCallback(ActionDay, strconv.FormatInt(serviceID, 10), key),
		})
		if len(row) == daysPerRow {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	rows = append(rows, []models.InlineKeyboardButton{{
		Text: "⬅️ К услугам", CallbackData: NewCallback(ActionBackToSvc),
	}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func slotsKeyboard(serviceID int64, slots []domain.Slot, loc *time.Location) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton

	for _, slot := range slots {
		row = append(row, models.InlineKeyboardButton{
			Text:         formatTime(slot.Start, loc),
			CallbackData: NewCallback(ActionSlot, strconv.FormatInt(slot.ID, 10)),
		})
		if len(row) == slotsPerRow {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	rows = append(rows, []models.InlineKeyboardButton{{
		Text:         "⬅️ К дням",
		CallbackData: NewCallback(ActionBackToDay, strconv.FormatInt(serviceID, 10)),
	}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func confirmKeyboard(slotID, serviceID int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "✅ Подтвердить", CallbackData: NewCallback(ActionConfirm, strconv.FormatInt(slotID, 10))}},
		{{Text: "⬅️ К слотам", CallbackData: NewCallback(ActionBackToDay, strconv.FormatInt(serviceID, 10))}},
	}}
}

func myBookingsKeyboard(bookings []domain.Booking, loc *time.Location) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(bookings))
	for _, b := range bookings {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "❌ Отменить " + formatDayShort(b.SlotStart, loc) + " " + formatTime(b.SlotStart, loc),
			CallbackData: NewCallback(ActionCancel, strconv.FormatInt(b.ID, 10)),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func adminDaysKeyboard(days []time.Time, loc *time.Location) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton

	for _, day := range days {
		row = append(row, models.InlineKeyboardButton{
			Text:         formatDayShort(day, loc),
			CallbackData: NewCallback(ActionAdminDay, domain.FormatDay(day, loc)),
		})
		if len(row) == daysPerRow {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func adminCancelKeyboard(bookings []domain.Booking, loc *time.Location) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(bookings))
	for _, b := range bookings {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "❌ #" + strconv.FormatInt(b.ID, 10) + " " + formatTime(b.SlotStart, loc) + " " + b.ClientName,
			CallbackData: NewCallback(ActionAdminCancel, strconv.FormatInt(b.ID, 10)),
		}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

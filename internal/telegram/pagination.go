package telegram

import (
	"fmt"
	"strconv"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

// paginate возвращает элементы страницы, зажатый в допустимый диапазон номер
// страницы и общее число страниц. Зажатый page возвращается наружу, чтобы
// подпись «Страница N» и навигация опирались на реально показанную страницу, а не
// на исходный номер из callback — кнопка из старого сообщения может сослаться на
// страницу, которой уже нет.
func paginate[T any](items []T, page, size int) (pageItems []T, clampedPage, totalPages int) {
	if size <= 0 {
		return items, 0, 1
	}

	totalPages = (len(items) + size - 1) / size
	if totalPages == 0 {
		return nil, 0, 0
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * size
	end := min(start+size, len(items))
	return items[start:end], page, totalPages
}

// adminPageKeyboard — кнопки отмены для записей текущей страницы плюс навигация.
// Пагинация нужна не только из-за лимита в 4096 символов: Telegram ограничивает
// и количество кнопок в клавиатуре, а записей на день может быть много.
func adminPageKeyboard(pageItems []domain.Booking, day time.Time, page, totalPages int, loc *time.Location) *models.InlineKeyboardMarkup {
	kb := adminCancelKeyboard(pageItems, loc)
	if totalPages <= 1 {
		return kb
	}

	dayKey := domain.FormatDay(day, loc)
	var nav []models.InlineKeyboardButton

	if page > 0 {
		nav = append(nav, models.InlineKeyboardButton{
			Text:         "⬅️ Назад",
			CallbackData: NewCallback(ActionAdminDay, dayKey, strconv.Itoa(page-1)),
		})
	}
	nav = append(nav, models.InlineKeyboardButton{
		Text:         fmt.Sprintf("%d/%d", page+1, totalPages),
		CallbackData: NewCallback(ActionNoop),
	})
	if page < totalPages-1 {
		nav = append(nav, models.InlineKeyboardButton{
			Text:         "Далее ➡️",
			CallbackData: NewCallback(ActionAdminDay, dayKey, strconv.Itoa(page+1)),
		})
	}

	kb.InlineKeyboard = append(kb.InlineKeyboard, nav)
	return kb
}

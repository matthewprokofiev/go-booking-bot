package telegram

import (
	"testing"
	"time"

	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		name           string
		items          []int
		page           int
		size           int
		want           []int
		wantPage       int
		wantTotalPages int
	}{
		{name: "первая страница", items: items, page: 0, size: 3, want: []int{1, 2, 3}, wantPage: 0, wantTotalPages: 4},
		{name: "средняя страница", items: items, page: 1, size: 3, want: []int{4, 5, 6}, wantPage: 1, wantTotalPages: 4},
		{name: "неполная последняя страница", items: items, page: 3, size: 3, want: []int{10}, wantPage: 3, wantTotalPages: 4},
		{name: "всё влезает на страницу", items: items, page: 0, size: 20, want: items, wantPage: 0, wantTotalPages: 1},
		{name: "ровное деление", items: items, page: 1, size: 5, want: []int{6, 7, 8, 9, 10}, wantPage: 1, wantTotalPages: 2},

		// Кнопка из старого сообщения может указывать на несуществующую страницу:
		// paginate обязан вернуть зажатый номер, а не исходный.
		{name: "страница за пределами схлопывается к последней", items: items, page: 99, size: 3, want: []int{10}, wantPage: 3, wantTotalPages: 4},
		{name: "отрицательная страница схлопывается к первой", items: items, page: -5, size: 3, want: []int{1, 2, 3}, wantPage: 0, wantTotalPages: 4},

		{name: "пустой список", items: nil, page: 0, size: 3, want: nil, wantPage: 0, wantTotalPages: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, page, totalPages := paginate(tt.items, tt.page, tt.size)

			if totalPages != tt.wantTotalPages {
				t.Errorf("totalPages = %d, ожидалось %d", totalPages, tt.wantTotalPages)
			}
			if page != tt.wantPage {
				t.Errorf("зажатый page = %d, ожидалось %d", page, tt.wantPage)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("страница = %v, ожидалось %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("страница = %v, ожидалось %v", got, tt.want)
				}
			}
		})
	}
}

// Каждый элемент должен попасть ровно на одну страницу.
func TestPaginateCoversAllItemsExactlyOnce(t *testing.T) {
	items := make([]int, 25)
	for i := range items {
		items[i] = i
	}

	const size = 8
	_, _, totalPages := paginate(items, 0, size)

	seen := make(map[int]int)
	for page := 0; page < totalPages; page++ {
		pageItems, _, _ := paginate(items, page, size)
		for _, it := range pageItems {
			seen[it]++
		}
	}

	if len(seen) != len(items) {
		t.Fatalf("покрыто элементов: %d, ожидалось %d", len(seen), len(items))
	}
	for item, count := range seen {
		if count != 1 {
			t.Errorf("элемент %d встретился %d раз, ожидался ровно 1", item, count)
		}
	}
}

func TestAdminPageKeyboardNavigation(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, msk)
	bookings := []domain.Booking{{ID: 1, SlotStart: day.Add(10 * time.Hour), ClientName: "Иван"}}

	tests := []struct {
		name       string
		page       int
		totalPages int
		wantBack   bool
		wantNext   bool
	}{
		{name: "одна страница — навигации нет", page: 0, totalPages: 1},
		{name: "первая из трёх", page: 0, totalPages: 3, wantNext: true},
		{name: "средняя из трёх", page: 1, totalPages: 3, wantBack: true, wantNext: true},
		{name: "последняя из трёх", page: 2, totalPages: 3, wantBack: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := adminPageKeyboard(bookings, day, tt.page, tt.totalPages, msk)

			var hasBack, hasNext bool
			for _, row := range kb.InlineKeyboard {
				for _, btn := range row {
					switch btn.Text {
					case "⬅️ Назад":
						hasBack = true
					case "Далее ➡️":
						hasNext = true
					}
					if len(btn.CallbackData) > 64 {
						t.Errorf("callback_data кнопки %q длиной %d превышает лимит в 64 байта",
							btn.Text, len(btn.CallbackData))
					}
				}
			}

			if hasBack != tt.wantBack {
				t.Errorf("кнопка «Назад»: %v, ожидалось %v", hasBack, tt.wantBack)
			}
			if hasNext != tt.wantNext {
				t.Errorf("кнопка «Далее»: %v, ожидалось %v", hasNext, tt.wantNext)
			}
		})
	}
}

package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name      string
		lines     []string
		limit     int
		wantCount int
		wantFirst string
	}{
		{name: "пустой список", lines: nil, limit: 100, wantCount: 0},
		{
			name: "влезает в одно сообщение", lines: []string{"строка 1", "строка 2"},
			limit: 100, wantCount: 1, wantFirst: "строка 1\nстрока 2",
		},
		{
			// "aaaa" + "\n" + "bbbb" = 9 > 8, значит два сообщения.
			name: "перенос по границе строки", lines: []string{"aaaa", "bbbb"},
			limit: 8, wantCount: 2, wantFirst: "aaaa",
		},
		{
			name: "ровно по лимиту одним сообщением", lines: []string{"aaaa", "bbb"},
			limit: 8, wantCount: 1, wantFirst: "aaaa\nbbb",
		},
		{
			name:  "много строк разбиваются на страницы",
			lines: []string{"aaaa", "bbbb", "cccc", "dddd"},
			limit: 9, wantCount: 2, wantFirst: "aaaa\nbbbb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitMessage(tt.lines, tt.limit)

			if len(got) != tt.wantCount {
				t.Fatalf("количество сообщений = %d, ожидалось %d (%q)", len(got), tt.wantCount, got)
			}
			if tt.wantCount > 0 && got[0] != tt.wantFirst {
				t.Errorf("первое сообщение = %q, ожидалось %q", got[0], tt.wantFirst)
			}
			for _, m := range got {
				if len(m) > tt.limit {
					t.Errorf("сообщение длиной %d превысило лимит %d", len(m), tt.limit)
				}
			}
		})
	}
}

// Ни одна строка не должна потеряться при разбиении.
func TestSplitMessagePreservesAllLines(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "10:00 — Окрашивание, клиент Иван Иванов"
	}

	got := SplitMessage(lines, splitLimit)

	if len(got) < 2 {
		t.Fatalf("ожидалось несколько сообщений, получено %d", len(got))
	}

	var total int
	for _, m := range got {
		if len(m) > splitLimit {
			t.Errorf("сообщение длиной %d превысило лимит %d", len(m), splitLimit)
		}
		if len(m) > TelegramMaxMessageLen {
			t.Errorf("сообщение длиной %d превысило лимит Telegram", len(m))
		}
		total += len(strings.Split(m, "\n"))
	}
	if total != len(lines) {
		t.Errorf("сохранено строк: %d, ожидалось %d", total, len(lines))
	}
}

func TestSplitMessageForcesCutOnOverlongLine(t *testing.T) {
	long := strings.Repeat("a", 250)

	got := SplitMessage([]string{long}, 100)

	if len(got) != 3 {
		t.Fatalf("количество сообщений = %d, ожидалось 3", len(got))
	}
	if joined := strings.Join(got, ""); joined != long {
		t.Error("принудительный рез потерял часть строки")
	}
}

// Рез длинной кириллической строки не должен разрывать многобайтовый символ.
func TestSplitMessageKeepsUTF8Valid(t *testing.T) {
	long := strings.Repeat("я", 200) // 2 байта на символ

	got := SplitMessage([]string{long}, 101) // лимит намеренно нечётный

	if len(got) < 2 {
		t.Fatalf("ожидалось несколько сообщений, получено %d", len(got))
	}
	for i, m := range got {
		if !utf8.ValidString(m) {
			t.Errorf("сообщение %d содержит битый UTF-8", i)
		}
	}
	if joined := strings.Join(got, ""); joined != long {
		t.Error("рез по границе UTF-8 потерял или исказил текст")
	}
}

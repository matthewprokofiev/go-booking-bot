package telegram

import "testing"

func TestNewCallbackAndParseRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		args       []string
		wantData   string
		wantArgLen int
	}{
		{name: "без аргументов", action: ActionBackToSvc, args: nil, wantData: "bsvc", wantArgLen: 0},
		{name: "один аргумент", action: ActionService, args: []string{"7"}, wantData: "svc:7", wantArgLen: 1},
		{name: "два аргумента", action: ActionDay, args: []string{"7", "2026-07-17"}, wantData: "day:7:2026-07-17", wantArgLen: 2},
		{name: "страница админа", action: ActionAdminDay, args: []string{"2026-07-17", "2"}, wantData: "aday:2026-07-17:2", wantArgLen: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := NewCallback(tt.action, tt.args...)
			if data != tt.wantData {
				t.Fatalf("NewCallback = %q, ожидалось %q", data, tt.wantData)
			}

			// Telegram обрежет callback_data длиннее 64 байт, и кнопка сломается.
			if len(data) > 64 {
				t.Errorf("callback_data длиной %d превышает лимит Telegram в 64 байта", len(data))
			}

			cb, err := ParseCallback(data)
			if err != nil {
				t.Fatalf("ParseCallback(%q): %v", data, err)
			}
			if cb.Action != tt.action {
				t.Errorf("Action = %q, ожидалось %q", cb.Action, tt.action)
			}
			if len(cb.Args) != tt.wantArgLen {
				t.Errorf("количество аргументов = %d, ожидалось %d", len(cb.Args), tt.wantArgLen)
			}
			for i, want := range tt.args {
				if got := cb.Arg(i); got != want {
					t.Errorf("Arg(%d) = %q, ожидалось %q", i, got, want)
				}
			}
		})
	}
}

func TestParseCallbackEmpty(t *testing.T) {
	if _, err := ParseCallback(""); err == nil {
		t.Error("ParseCallback(\"\"): ожидалась ошибка")
	}
}

func TestCallbackArgOutOfRange(t *testing.T) {
	cb, err := ParseCallback("svc:7")
	if err != nil {
		t.Fatalf("ParseCallback: %v", err)
	}

	if got := cb.Arg(5); got != "" {
		t.Errorf("Arg(5) = %q, ожидалась пустая строка", got)
	}
	if _, err := cb.IntArg(5); err == nil {
		t.Error("IntArg(5): ожидалась ошибка для отсутствующего аргумента")
	}
}

func TestCallbackIntArg(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		index   int
		want    int64
		wantErr bool
	}{
		{name: "число", data: "svc:42", index: 0, want: 42},
		{name: "большой id", data: "cxl:9007199254740993", index: 0, want: 9007199254740993},
		{name: "не число", data: "svc:abc", index: 0, wantErr: true},
		{name: "дата вместо числа", data: "day:2026-07-17", index: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, err := ParseCallback(tt.data)
			if err != nil {
				t.Fatalf("ParseCallback(%q): %v", tt.data, err)
			}

			got, err := cb.IntArg(tt.index)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("IntArg(%d) для %q: ожидалась ошибка, получено %d", tt.index, tt.data, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("IntArg(%d): неожиданная ошибка: %v", tt.index, err)
			}
			if got != tt.want {
				t.Errorf("IntArg(%d) = %d, ожидалось %d", tt.index, got, tt.want)
			}
		})
	}
}

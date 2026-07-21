package config

import (
	"testing"
)

func TestParseAdminIDs(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []int64
		wantErr bool
	}{
		{name: "пусто", raw: "", want: nil},
		{name: "только пробелы", raw: "   ", want: nil},
		{name: "один id", raw: "123456789", want: []int64{123456789}},
		{name: "несколько id", raw: "1,2,3", want: []int64{1, 2, 3}},
		{name: "пробелы вокруг id", raw: " 1 , 2 ", want: []int64{1, 2}},
		{name: "висящая запятая", raw: "1,2,", want: []int64{1, 2}},
		{name: "дубликаты схлопываются", raw: "5,5,7", want: []int64{5, 7}},
		{name: "не число", raw: "1,abc", wantErr: true},
		{name: "отрицательный", raw: "-1", wantErr: true},
		{name: "ноль", raw: "0", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAdminIDs(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseAdminIDs(%q): ожидалась ошибка, получено %v", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAdminIDs(%q): неожиданная ошибка: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseAdminIDs(%q) = %v, ожидалось %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ParseAdminIDs(%q) = %v, ожидалось %v", tt.raw, got, tt.want)
				}
			}
		})
	}
}

func TestLoadFailsWithoutRequiredEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "нет BOT_TOKEN", env: map[string]string{"DATABASE_URL": "postgres://localhost/db"}},
		{name: "нет DATABASE_URL", env: map[string]string{"BOT_TOKEN": "token"}},
		{name: "нет ничего", env: map[string]string{}},
		{name: "битая таймзона", env: map[string]string{
			"BOT_TOKEN": "token", "DATABASE_URL": "postgres://localhost/db", "BUSINESS_TZ": "Mars/Olympus",
		}},
		{name: "рабочие часы наоборот", env: map[string]string{
			"BOT_TOKEN": "token", "DATABASE_URL": "postgres://localhost/db",
			"WORK_HOURS_START": "20", "WORK_HOURS_END": "10",
		}},
		{name: "час вне диапазона", env: map[string]string{
			"BOT_TOKEN": "token", "DATABASE_URL": "postgres://localhost/db", "WORK_HOURS_END": "42",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load(): ожидалась ошибка конфигурации, получено nil")
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BOT_TOKEN", "token")
	t.Setenv("DATABASE_URL", "postgres://localhost/db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): неожиданная ошибка: %v", err)
	}
	if cfg.BusinessTZ.String() != defaultBusinessTZ {
		t.Errorf("BusinessTZ = %q, ожидалось %q", cfg.BusinessTZ, defaultBusinessTZ)
	}
	if cfg.WorkHoursStart != defaultWorkHoursStart || cfg.WorkHoursEnd != defaultWorkHoursEnd {
		t.Errorf("рабочие часы = %d-%d, ожидалось %d-%d",
			cfg.WorkHoursStart, cfg.WorkHoursEnd, defaultWorkHoursStart, defaultWorkHoursEnd)
	}
	if cfg.AppEnv != EnvLocal {
		t.Errorf("AppEnv = %q, ожидалось %q", cfg.AppEnv, EnvLocal)
	}
}

func TestIsAdmin(t *testing.T) {
	cfg := Config{AdminTgIDs: []int64{10, 20}}

	if !cfg.IsAdmin(20) {
		t.Error("IsAdmin(20) = false, ожидалось true")
	}
	if cfg.IsAdmin(30) {
		t.Error("IsAdmin(30) = true, ожидалось false")
	}
	if (Config{}).IsAdmin(10) {
		t.Error("IsAdmin при пустом списке админов = true, ожидалось false")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BOT_TOKEN", "DATABASE_URL", "ADMIN_TG_IDS",
		"BUSINESS_TZ", "WORK_HOURS_START", "WORK_HOURS_END", "APP_ENV",
	} {
		t.Setenv(k, "")
	}
}

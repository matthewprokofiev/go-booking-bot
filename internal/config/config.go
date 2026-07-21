package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvLocal = "local"

	defaultBusinessTZ     = "Europe/Moscow"
	defaultWorkHoursStart = 10
	defaultWorkHoursEnd   = 20
)

type Config struct {
	BotToken       string
	DatabaseURL    string
	AdminTgIDs     []int64
	BusinessTZ     *time.Location
	WorkHoursStart int
	WorkHoursEnd   int
	AppEnv         string
}

func (c Config) IsAdmin(tgID int64) bool {
	for _, id := range c.AdminTgIDs {
		if id == tgID {
			return true
		}
	}
	return false
}

// Load собирает конфиг из ENV и падает при отсутствии критичных переменных:
// бот без токена или без БД всё равно нежизнеспособен, лучше узнать это на старте.
func Load() (Config, error) {
	var cfg Config
	var problems []string

	cfg.BotToken = os.Getenv("BOT_TOKEN")
	if cfg.BotToken == "" {
		problems = append(problems, "BOT_TOKEN не задан: получите токен у @BotFather")
	}

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL не задан: например postgres://user:pass@host:5432/db?sslmode=disable")
	}

	adminIDs, err := ParseAdminIDs(os.Getenv("ADMIN_TG_IDS"))
	if err != nil {
		problems = append(problems, fmt.Sprintf("ADMIN_TG_IDS: %v", err))
	}
	cfg.AdminTgIDs = adminIDs

	tzName := envOrDefault("BUSINESS_TZ", defaultBusinessTZ)
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		problems = append(problems, fmt.Sprintf("BUSINESS_TZ=%q: неизвестная таймзона", tzName))
	}
	cfg.BusinessTZ = loc

	cfg.WorkHoursStart, err = envHour("WORK_HOURS_START", defaultWorkHoursStart)
	if err != nil {
		problems = append(problems, err.Error())
	}

	cfg.WorkHoursEnd, err = envHour("WORK_HOURS_END", defaultWorkHoursEnd)
	if err != nil {
		problems = append(problems, err.Error())
	}

	if cfg.WorkHoursStart >= cfg.WorkHoursEnd {
		problems = append(problems, fmt.Sprintf(
			"WORK_HOURS_START (%d) должен быть меньше WORK_HOURS_END (%d)", cfg.WorkHoursStart, cfg.WorkHoursEnd))
	}

	cfg.AppEnv = envOrDefault("APP_ENV", EnvLocal)

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("некорректная конфигурация:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

func ParseAdminIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var ids []int64
	seen := make(map[int64]struct{})

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q не является числовым tg_id", part)
		}
		if id <= 0 {
			return nil, fmt.Errorf("%q: tg_id должен быть положительным", part)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func NewLogger(appEnv string) *slog.Logger {
	if appEnv == EnvLocal {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envHour(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	h, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s=%q: ожидается число (час 0-23)", key, raw)
	}
	if h < 0 || h > 23 {
		return fallback, fmt.Errorf("%s=%d: час должен быть в диапазоне 0-23", key, h)
	}
	return h, nil
}

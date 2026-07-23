package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-booking-bot/internal/config"
	"github.com/matveiprokofev/go-booking-bot/internal/storage"
	"github.com/matveiprokofev/go-booking-bot/internal/telegram"
)

// shutdownGrace — сколько ждём завершения активных обработчиков после сигнала
// перед закрытием пула соединений.
const shutdownGrace = 10 * time.Second

func main() {
	if err := run(); err != nil {
		// Логгер может быть ещё не создан (падение на конфиге), поэтому пишем в stderr.
		fmt.Fprintf(os.Stderr, "фатальная ошибка: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := config.NewLogger(cfg.AppEnv)
	log.Info("запуск бота",
		"app_env", cfg.AppEnv,
		"business_tz", cfg.BusinessTZ.String(),
		"work_hours", fmt.Sprintf("%d-%d", cfg.WorkHoursStart, cfg.WorkHoursEnd),
		"admins", len(cfg.AdminTgIDs))

	if len(cfg.AdminTgIDs) == 0 {
		log.Warn("ADMIN_TG_IDS пуст: уведомления о новых записях отправлять некому")
	}

	// ctx поллинга: по SIGINT/SIGTERM отменяется и гасит long polling и стартовые
	// операции (миграции/сид) — без него бот висел бы на getUpdates.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("миграции: %w", err)
	}
	log.Info("миграции применены")

	db, err := storage.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return fmt.Errorf("подключение к БД: %w", err)
	}
	defer db.Close()

	if err := db.Seed(ctx, cfg.BusinessTZ, cfg.WorkHoursStart, cfg.WorkHoursEnd, time.Now()); err != nil {
		return fmt.Errorf("сид демо-данных: %w", err)
	}

	if err := db.SyncAdmins(ctx, cfg.AdminTgIDs); err != nil {
		return fmt.Errorf("синхронизация админов: %w", err)
	}

	// opCtx не привязан к сигналу: когда ctx поллинга отменится по SIGTERM, уже
	// начатая обработка апдейта (в т.ч. транзакция брони) должна дописаться, а не
	// оборваться. opCtx отменяется ниже, после того как хендлеры завершились.
	opCtx, opCancel := context.WithCancel(context.Background())
	defer opCancel()

	handler := telegram.NewHandler(opCtx, cfg, db, log)

	opts := append(handler.Options(), bot.WithErrorsHandler(func(err error) {
		log.Error("ошибка Telegram API", "error", err)
	}))

	b, err := bot.New(cfg.BotToken, opts...)
	if err != nil {
		return fmt.Errorf("создание бота: %w", err)
	}

	setBotCommands(ctx, b, log)

	log.Info("бот запущен, начинаю long polling")
	b.Start(ctx) // блокируется до отмены ctx

	// Порядок остановки: поллинг уже встал → ждём догрестись in-flight хендлерам →
	// только потом (через defer opCancel и db.Close) рвём операции и закрываем пул.
	log.Info("получен сигнал остановки, дожидаюсь активных обработчиков")
	handler.Wait(shutdownGrace)
	log.Info("завершаюсь")
	return nil
}

func setBotCommands(ctx context.Context, b *bot.Bot, log *slog.Logger) {
	_, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "start", Description: "Главное меню"},
			{Command: "book", Description: "Записаться"},
			{Command: "my", Description: "Мои записи"},
			{Command: "day", Description: "Записи на день (админ)"},
		},
	})
	if err != nil {
		// Не критично: бот работает и без подсказок команд в интерфейсе Telegram.
		log.Warn("установка списка команд", "error", err)
	}
}

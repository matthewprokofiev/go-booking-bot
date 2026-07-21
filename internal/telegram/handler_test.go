package telegram

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-booking-bot/internal/config"
)

func testHandler(opCtx context.Context) *Handler {
	return NewHandler(opCtx, config.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// Wait должен блокироваться, пока обработчик апдейта не завершится: иначе main
// закроет пул БД под работающей горутиной (баг из ревью про graceful shutdown).
func TestWaitBlocksUntilHandlerDone(t *testing.T) {
	h := testHandler(context.Background())

	release := make(chan struct{})
	started := make(chan struct{})

	wrapped := h.processMiddleware(func(_ context.Context, _ *bot.Bot, _ *models.Update) {
		close(started)
		<-release
	})

	go wrapped(context.Background(), nil, &models.Update{ID: 1})

	<-started // обработчик точно в работе

	waitReturned := make(chan struct{})
	go func() {
		h.Wait(2 * time.Second)
		close(waitReturned)
	}()

	select {
	case <-waitReturned:
		t.Fatal("Wait вернулся, пока обработчик ещё работает")
	case <-time.After(100 * time.Millisecond):
	}

	close(release) // отпускаем обработчик

	select {
	case <-waitReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait не вернулся после завершения обработчика")
	}
}

// Wait не должен висеть дольше grace-таймаута, даже если обработчик застрял.
func TestWaitHonorsTimeout(t *testing.T) {
	h := testHandler(context.Background())

	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})

	wrapped := h.processMiddleware(func(_ context.Context, _ *bot.Bot, _ *models.Update) {
		close(started)
		<-release
	})
	go wrapped(context.Background(), nil, &models.Update{ID: 1})
	<-started

	start := time.Now()
	h.Wait(150 * time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Wait ждал %v, ожидался выход по таймауту ~150ms", elapsed)
	}
}

// Обработчик должен работать в opCtx, а не в ctx поллинга: по SIGTERM ctx поллинга
// отменяется, но начатую бронь нужно дописать.
func TestHandlerRunsInOpCtxNotPollingCtx(t *testing.T) {
	type ctxKey string
	const key ctxKey = "which"

	opCtx := context.WithValue(context.Background(), key, "opCtx")
	h := testHandler(opCtx)

	// ctx поллинга уже отменён — имитируем пришедший SIGTERM.
	pollingCtx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "pollingCtx"))
	cancel()

	got := make(chan any, 1)
	wrapped := h.processMiddleware(func(ctx context.Context, _ *bot.Bot, _ *models.Update) {
		got <- ctx.Value(key)
		if err := ctx.Err(); err != nil {
			t.Errorf("ctx обработчика уже отменён: %v", err)
		}
	})

	wrapped(pollingCtx, nil, &models.Update{ID: 1})

	if v := <-got; v != "opCtx" {
		t.Errorf("обработчик получил ctx=%v, ожидался opCtx", v)
	}
}

// Паника в обработчике не должна пробиваться наружу и ронять процесс.
func TestProcessMiddlewareRecoversPanic(t *testing.T) {
	h := testHandler(context.Background())

	wrapped := h.processMiddleware(func(_ context.Context, _ *bot.Bot, _ *models.Update) {
		panic("бум")
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("паника пробилась наружу middleware: %v", r)
		}
	}()

	// Update без Message/CallbackQuery: chatIDFromUpdate вернёт 0, отправки не будет
	// (nil-бот не дёрнется), проверяем именно перехват паники.
	wrapped(context.Background(), nil, &models.Update{ID: 1})

	// И WaitGroup должна быть освобождена, несмотря на панику.
	h.Wait(time.Second)
}

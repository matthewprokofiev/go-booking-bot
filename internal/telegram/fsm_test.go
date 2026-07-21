package telegram

import (
	"sync"
	"testing"
)

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name string
		from Step
		to   Step
		want bool
	}{
		{name: "начало записи", from: StepIdle, to: StepChoosingSvc, want: true},
		{name: "услуга → день", from: StepChoosingSvc, to: StepChoosingDay, want: true},
		{name: "день → слот", from: StepChoosingDay, to: StepChoosingSlot, want: true},
		{name: "слот → подтверждение", from: StepChoosingSlot, to: StepConfirming, want: true},
		{name: "подтверждение → idle", from: StepConfirming, to: StepIdle, want: true},

		{name: "шаг назад к выбору дня", from: StepChoosingSlot, to: StepChoosingDay, want: true},
		{name: "шаг назад к выбору услуги", from: StepChoosingDay, to: StepChoosingSvc, want: true},

		// Слоты могли разобрать, пока клиент выбирал: нужно перерисовать тот же шаг.
		{name: "перерисовка выбора дня", from: StepChoosingDay, to: StepChoosingDay, want: true},
		{name: "перерисовка выбора слота", from: StepChoosingSlot, to: StepChoosingSlot, want: true},

		// Проиграл гонку за слот на подтверждении — возвращаем к выбору дня, а не в тупик.
		{name: "с подтверждения назад к дням", from: StepConfirming, to: StepChoosingDay, want: true},

		// Ключевая защита: кнопка из старого сообщения не должна протаскивать
		// диалог через пропущенные шаги.
		{name: "нельзя перескочить с idle на день", from: StepIdle, to: StepChoosingDay, want: false},
		{name: "нельзя перескочить с idle на слот", from: StepIdle, to: StepChoosingSlot, want: false},
		{name: "нельзя подтвердить из idle", from: StepIdle, to: StepConfirming, want: false},
		{name: "нельзя с услуги сразу на слот", from: StepChoosingSvc, to: StepChoosingSlot, want: false},
		{name: "нельзя с услуги сразу на подтверждение", from: StepChoosingSvc, to: StepConfirming, want: false},
		{name: "нельзя с дня сразу на подтверждение", from: StepChoosingDay, to: StepConfirming, want: false},
		{name: "неизвестный шаг никуда не ведёт", from: Step("bogus"), to: StepChoosingSvc, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%s, %s) = %v, ожидалось %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestStoreDefaultsToIdle(t *testing.T) {
	store := NewStore()

	if got := store.Get(42); got.Step != StepIdle {
		t.Errorf("состояние нового чата = %s, ожидалось %s", got.Step, StepIdle)
	}
}

func TestStoreAdvanceHappyPath(t *testing.T) {
	store := NewStore()
	const chatID int64 = 42

	steps := []State{
		{Step: StepChoosingSvc},
		{Step: StepChoosingDay, ServiceID: 1},
		{Step: StepChoosingSlot, ServiceID: 1, Day: "2026-07-17"},
		{Step: StepConfirming, ServiceID: 1, Day: "2026-07-17", SlotID: 7},
	}

	for _, next := range steps {
		if _, err := store.Advance(chatID, next); err != nil {
			t.Fatalf("Advance(%s): неожиданная ошибка: %v", next.Step, err)
		}
	}

	got := store.Get(chatID)
	if got.Step != StepConfirming || got.SlotID != 7 || got.ServiceID != 1 || got.Day != "2026-07-17" {
		t.Errorf("итоговое состояние = %+v, ожидалось подтверждение слота 7", got)
	}
}

// Сценарий проигранной гонки: клиент дошёл до подтверждения, слот увели,
// и его возвращают к выбору дня той же услуги. Диалог не должен превратиться в тупик.
func TestStoreRecoversAfterLosingSlotRace(t *testing.T) {
	store := NewStore()
	const chatID int64 = 42

	mustAdvance(t, store, chatID, State{Step: StepChoosingSvc})
	mustAdvance(t, store, chatID, State{Step: StepChoosingDay, ServiceID: 1})
	mustAdvance(t, store, chatID, State{Step: StepChoosingSlot, ServiceID: 1, Day: "2026-07-17"})
	mustAdvance(t, store, chatID, State{Step: StepConfirming, ServiceID: 1, Day: "2026-07-17", SlotID: 7})

	// Слот увели — возвращаемся к выбору дня, услуга сохраняется.
	mustAdvance(t, store, chatID, State{Step: StepChoosingDay, ServiceID: 1})

	got := store.Get(chatID)
	if got.Step != StepChoosingDay {
		t.Fatalf("шаг после проигранной гонки = %s, ожидался %s", got.Step, StepChoosingDay)
	}
	if got.ServiceID != 1 {
		t.Errorf("ServiceID = %d, ожидался 1: услуга должна сохраниться", got.ServiceID)
	}

	// И клиент может спокойно добронировать другой слот.
	mustAdvance(t, store, chatID, State{Step: StepChoosingSlot, ServiceID: 1, Day: "2026-07-18"})
	mustAdvance(t, store, chatID, State{Step: StepConfirming, ServiceID: 1, Day: "2026-07-18", SlotID: 9})
}

// Список слотов мог опустеть, пока клиент выбирал: тот же шаг нужно уметь перерисовать.
func TestStoreAllowsRedrawOfCurrentStep(t *testing.T) {
	store := NewStore()
	const chatID int64 = 42

	mustAdvance(t, store, chatID, State{Step: StepChoosingSvc})
	mustAdvance(t, store, chatID, State{Step: StepChoosingDay, ServiceID: 1})
	mustAdvance(t, store, chatID, State{Step: StepChoosingDay, ServiceID: 1})

	if got := store.Get(chatID); got.Step != StepChoosingDay {
		t.Errorf("шаг после перерисовки = %s, ожидался %s", got.Step, StepChoosingDay)
	}
}

func TestStoreAdvanceRejectsIllegalTransition(t *testing.T) {
	store := NewStore()
	const chatID int64 = 42

	if _, err := store.Advance(chatID, State{Step: StepConfirming, SlotID: 7}); err == nil {
		t.Fatal("Advance(idle → confirming): ожидалась ошибка")
	}
	if got := store.Get(chatID); got.Step != StepIdle {
		t.Errorf("после отклонённого перехода состояние = %s, ожидалось %s", got.Step, StepIdle)
	}
}

// Возврат в idle должен очищать выбор, иначе следующая запись начнётся
// с подставленной услугой и слотом от прошлого диалога.
func TestStoreAdvanceToIdleClearsState(t *testing.T) {
	store := NewStore()
	const chatID int64 = 42

	mustAdvance(t, store, chatID, State{Step: StepChoosingSvc})
	mustAdvance(t, store, chatID, State{Step: StepChoosingDay, ServiceID: 3})
	mustAdvance(t, store, chatID, State{Step: StepIdle})

	got := store.Get(chatID)
	if got.Step != StepIdle || got.ServiceID != 0 || got.SlotID != 0 || got.Day != "" {
		t.Errorf("состояние после возврата в idle = %+v, ожидалось пустое", got)
	}
}

func TestStoreReset(t *testing.T) {
	store := NewStore()
	const chatID int64 = 42

	mustAdvance(t, store, chatID, State{Step: StepChoosingSvc})
	store.Reset(chatID)

	if got := store.Get(chatID); got.Step != StepIdle {
		t.Errorf("состояние после Reset = %s, ожидалось %s", got.Step, StepIdle)
	}
}

func TestStoreIsolatesChats(t *testing.T) {
	store := NewStore()

	mustAdvance(t, store, 1, State{Step: StepChoosingSvc})
	mustAdvance(t, store, 1, State{Step: StepChoosingDay, ServiceID: 10})
	mustAdvance(t, store, 2, State{Step: StepChoosingSvc})

	if got := store.Get(1); got.ServiceID != 10 {
		t.Errorf("чат 1: ServiceID = %d, ожидалось 10", got.ServiceID)
	}
	if got := store.Get(2); got.Step != StepChoosingSvc || got.ServiceID != 0 {
		t.Errorf("чат 2 = %+v, состояние протекло из чата 1", got)
	}
}

// Store читают и пишут горутины разных апдейтов — тест под -race.
func TestStoreConcurrentAccess(t *testing.T) {
	store := NewStore()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(chatID int64) {
			defer wg.Done()
			store.Advance(chatID, State{Step: StepChoosingSvc})
			store.Get(chatID)
			store.Advance(chatID, State{Step: StepChoosingDay, ServiceID: chatID})
			store.Reset(chatID)
		}(int64(i))
	}
	wg.Wait()
}

func mustAdvance(t *testing.T, store *Store, chatID int64, next State) {
	t.Helper()
	if _, err := store.Advance(chatID, next); err != nil {
		t.Fatalf("Advance(%s): %v", next.Step, err)
	}
}

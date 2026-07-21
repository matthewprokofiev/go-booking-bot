package telegram

import (
	"fmt"
	"sync"
)

type Step string

const (
	StepIdle         Step = "idle"
	StepChoosingSvc  Step = "choosing_service"
	StepChoosingDay  Step = "choosing_day"
	StepChoosingSlot Step = "choosing_slot"
	StepConfirming   Step = "confirming"
)

// State — прогресс диалога записи для одного чата.
type State struct {
	Step      Step
	ServiceID int64
	Day       string
	SlotID    int64
}

// allowedTransitions задаёт граф диалога: услуга → день → слот → подтверждение.
// Явный граф нужен, чтобы «залипшая» кнопка из старого сообщения не протащила
// диалог через пропущенный шаг: Telegram позволяет нажать инлайн-кнопку из истории
// в любой момент, и без проверки перехода мы получили бы бронь без выбранной услуги.
//
// Шаг назад и перерисовка текущего шага разрешены: список слотов мог опустеть,
// пока клиент выбирал, а после проигранной гонки за слот его нужно вернуть к выбору дня.
// Жёстко держатся два инварианта: в StepConfirming попадают только из StepChoosingSlot,
// а из StepIdle — только в StepChoosingSvc (запись всегда начинается с выбора услуги).
var allowedTransitions = map[Step][]Step{
	StepIdle:         {StepChoosingSvc},
	StepChoosingSvc:  {StepChoosingSvc, StepChoosingDay, StepIdle},
	StepChoosingDay:  {StepChoosingSvc, StepChoosingDay, StepChoosingSlot, StepIdle},
	StepChoosingSlot: {StepChoosingSvc, StepChoosingDay, StepChoosingSlot, StepConfirming, StepIdle},
	StepConfirming:   {StepChoosingSvc, StepChoosingDay, StepChoosingSlot, StepIdle},
}

func CanTransition(from, to Step) bool {
	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Store — состояния диалогов в памяти. Для демо этого достаточно: при рестарте
// пользователь просто начинает запись заново, а сами брони лежат в БД.
type Store struct {
	mu     sync.Mutex
	states map[int64]State
}

func NewStore() *Store {
	return &Store{states: make(map[int64]State)}
}

func (s *Store) Get(chatID int64) State {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.states[chatID]
	if !ok {
		return State{Step: StepIdle}
	}
	return st
}

// Advance переводит диалог в next, если такой переход разрешён графом.
func (s *Store) Advance(chatID int64, next State) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.states[chatID]
	if !ok {
		current = State{Step: StepIdle}
	}

	if !CanTransition(current.Step, next.Step) {
		return current, fmt.Errorf("переход %s → %s запрещён", current.Step, next.Step)
	}

	if next.Step == StepIdle {
		delete(s.states, chatID)
		return State{Step: StepIdle}, nil
	}

	s.states[chatID] = next
	return next, nil
}

func (s *Store) Reset(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.states, chatID)
}

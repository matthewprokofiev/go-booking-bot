package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-booking-bot/internal/config"
	"github.com/matveiprokofev/go-booking-bot/internal/domain"
)

const (
	bookingHorizonDays = 7
	adminPageSize      = 8
)

// Repo — то, что хендлерам нужно от хранилища.
type Repo interface {
	ActiveServices(ctx context.Context) ([]domain.Service, error)
	ServiceByID(ctx context.Context, id int64) (domain.Service, error)
	FreeSlotsForDay(ctx context.Context, serviceID int64, dayStart, dayEnd, notBefore time.Time) ([]domain.Slot, error)
	DaysWithFreeSlots(ctx context.Context, serviceID int64, from, to, notBefore time.Time, loc *time.Location) (map[string]int, error)
	SlotByID(ctx context.Context, id int64) (domain.Slot, error)
	CreateBooking(ctx context.Context, slotID, clientTgID int64, clientName string) (domain.Booking, error)
	CancelBooking(ctx context.Context, bookingID, byTgID int64) (domain.Booking, error)
	ActiveBookingsByClient(ctx context.Context, clientTgID int64, from time.Time) ([]domain.Booking, error)
	ActiveBookingsForDay(ctx context.Context, dayStart, dayEnd time.Time) ([]domain.Booking, error)
}

type Handler struct {
	cfg   config.Config
	repo  Repo
	store *Store
	log   *slog.Logger
	now   func() time.Time

	// opCtx отвязывает работу хендлеров от ctx поллинга. По SIGTERM ctx поллинга
	// отменяется, чтобы бот перестал забирать апдейты, — но начатую бронь нужно
	// дописать, а не оборвать транзакцию на середине. opCtx живёт до grace-таймаута
	// в main и отменяется уже после того, как in-flight хендлеры завершились.
	opCtx context.Context
	wg    sync.WaitGroup
}

func NewHandler(opCtx context.Context, cfg config.Config, repo Repo, log *slog.Logger) *Handler {
	return &Handler{
		cfg:   cfg,
		repo:  repo,
		store: NewStore(),
		log:   log,
		now:   time.Now,
		opCtx: opCtx,
	}
}

func (h *Handler) Options() []bot.Option {
	return []bot.Option{
		bot.WithDefaultHandler(h.handleUpdate),
		bot.WithMiddlewares(h.processMiddleware),
	}
}

// Wait блокируется, пока не завершатся все обрабатываемые сейчас апдейты либо не
// истечёт timeout. Вызывается в main после остановки поллинга и до закрытия пула,
// чтобы БД не закрылась под работающей горутиной.
func (h *Handler) Wait(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		h.log.Warn("не все обработчики успели завершиться за grace-период", "timeout", timeout)
	}
}

// processMiddleware делает три вещи: считает in-flight обработчики в WaitGroup (для
// graceful shutdown), подменяет ctx поллинга на opCtx (чтобы начатую операцию не
// оборвал сигнал) и ловит панику, не роняя процесс.
func (h *Handler) processMiddleware(next bot.HandlerFunc) bot.HandlerFunc {
	return func(_ context.Context, b *bot.Bot, update *models.Update) {
		h.wg.Add(1)
		defer h.wg.Done()

		ctx := h.opCtx

		defer func() {
			if r := recover(); r != nil {
				h.log.Error("паника в обработчике апдейта", "panic", r, "update_id", update.ID)

				if chatID := chatIDFromUpdate(update); chatID != 0 {
					h.send(ctx, b, chatID, "Что-то пошло не так. Попробуйте ещё раз: /start", nil)
				}
			}
		}()
		next(ctx, b, update)
	}
}

func (h *Handler) handleUpdate(ctx context.Context, b *bot.Bot, update *models.Update) {
	switch {
	case update.CallbackQuery != nil:
		h.handleCallback(ctx, b, update.CallbackQuery)
	case update.Message != nil:
		h.handleMessage(ctx, b, update.Message)
	}
}

func (h *Handler) handleMessage(ctx context.Context, b *bot.Bot, msg *models.Message) {
	chatID := msg.Chat.ID
	userID := userIDFromMessage(msg)

	switch msg.Text {
	case "/start":
		h.store.Reset(chatID)
		h.send(ctx, b, chatID, greeting(h.cfg.IsAdmin(userID)), mainMenuKeyboard(h.cfg.IsAdmin(userID)))

	case btnBook, "/book":
		h.showServices(ctx, b, chatID)

	case btnMyBookings, "/my":
		h.showMyBookings(ctx, b, chatID, userID)

	case btnAdminDay, "/day":
		if !h.cfg.IsAdmin(userID) {
			h.send(ctx, b, chatID, "Команда доступна только администратору.", nil)
			return
		}
		h.showAdminDayPicker(ctx, b, chatID)

	default:
		h.send(ctx, b, chatID, "Выберите действие в меню ниже или отправьте /start.", mainMenuKeyboard(h.cfg.IsAdmin(userID)))
	}
}

func (h *Handler) handleCallback(ctx context.Context, b *bot.Bot, cq *models.CallbackQuery) {
	if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: cq.ID}); err != nil {
		h.log.Warn("ответ на callback query", "error", err)
	}

	if cq.Message.Message == nil {
		return
	}
	chatID := cq.Message.Message.Chat.ID
	userID := cq.From.ID

	cb, err := ParseCallback(cq.Data)
	if err != nil {
		h.log.Warn("разбор callback_data", "error", err, "data", cq.Data)
		return
	}

	switch cb.Action {
	case ActionService:
		h.onServiceChosen(ctx, b, chatID, cb)
	case ActionDay:
		h.onDayChosen(ctx, b, chatID, cb)
	case ActionSlot:
		h.onSlotChosen(ctx, b, chatID, cb)
	case ActionConfirm:
		h.onConfirm(ctx, b, chatID, userID, cq, cb)
	case ActionBackToSvc:
		h.showServices(ctx, b, chatID)
	case ActionBackToDay:
		h.onBackToDays(ctx, b, chatID, cb)
	case ActionCancel:
		h.onClientCancel(ctx, b, chatID, userID, cb)
	case ActionAdminDay:
		h.onAdminDay(ctx, b, chatID, userID, cb)
	case ActionAdminCancel:
		h.onAdminCancel(ctx, b, chatID, userID, cb)
	case ActionNoop:
	default:
		h.log.Warn("неизвестное действие callback", "action", cb.Action)
	}
}

func (h *Handler) showServices(ctx context.Context, b *bot.Bot, chatID int64) {
	services, err := h.repo.ActiveServices(ctx)
	if err != nil {
		h.fail(ctx, b, chatID, "выборка услуг", err)
		return
	}
	if len(services) == 0 {
		h.send(ctx, b, chatID, "Услуги пока не заведены.", nil)
		return
	}

	if _, err := h.store.Advance(chatID, State{Step: StepChoosingSvc}); err != nil {
		// Возврат к выбору услуги разрешён почти отовсюду; если нет — сбрасываем диалог.
		h.store.Reset(chatID)
		if _, err := h.store.Advance(chatID, State{Step: StepChoosingSvc}); err != nil {
			h.fail(ctx, b, chatID, "начало записи", err)
			return
		}
	}

	h.send(ctx, b, chatID, "Выберите услугу:", servicesKeyboard(services))
}

func (h *Handler) onServiceChosen(ctx context.Context, b *bot.Bot, chatID int64, cb Callback) {
	serviceID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id услуги", "error", err)
		return
	}
	h.showDays(ctx, b, chatID, serviceID)
}

func (h *Handler) onBackToDays(ctx context.Context, b *bot.Bot, chatID int64, cb Callback) {
	serviceID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id услуги", "error", err)
		return
	}
	h.showDays(ctx, b, chatID, serviceID)
}

func (h *Handler) showDays(ctx context.Context, b *bot.Bot, chatID, serviceID int64) {
	svc, err := h.repo.ServiceByID(ctx, serviceID)
	if errors.Is(err, domain.ErrNotFound) {
		h.send(ctx, b, chatID, "Услуга не найдена. Начните заново: /start", nil)
		return
	}
	if err != nil {
		h.fail(ctx, b, chatID, "выборка услуги", err)
		return
	}

	loc := h.cfg.BusinessTZ
	now := h.now()
	days := domain.UpcomingDays(now, loc, bookingHorizonDays)
	from, _ := domain.DayBounds(days[0], loc)
	_, to := domain.DayBounds(days[len(days)-1], loc)

	freeByDay, err := h.repo.DaysWithFreeSlots(ctx, serviceID, from, to, now, loc)
	if err != nil {
		h.fail(ctx, b, chatID, "выборка свободных дней", err)
		return
	}
	if len(freeByDay) == 0 {
		h.send(ctx, b, chatID, "На ближайшую неделю свободных слотов нет.", nil)
		return
	}

	if _, err := h.store.Advance(chatID, State{Step: StepChoosingDay, ServiceID: serviceID}); err != nil {
		h.rejectStaleButton(ctx, b, chatID, err)
		return
	}

	text := fmt.Sprintf("%s\n\nВыберите день (в скобках — свободных слотов):", formatService(svc))
	h.send(ctx, b, chatID, text, daysKeyboard(serviceID, days, freeByDay, loc))
}

func (h *Handler) onDayChosen(ctx context.Context, b *bot.Bot, chatID int64, cb Callback) {
	serviceID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id услуги", "error", err)
		return
	}
	dayRaw := cb.Arg(1)

	loc := h.cfg.BusinessTZ
	day, err := domain.ParseDay(dayRaw, loc)
	if err != nil {
		h.log.Warn("некорректная дата в callback", "error", err, "day", dayRaw)
		return
	}

	dayStart, dayEnd := domain.DayBounds(day, loc)
	slots, err := h.repo.FreeSlotsForDay(ctx, serviceID, dayStart, dayEnd, h.now())
	if err != nil {
		h.fail(ctx, b, chatID, "выборка слотов", err)
		return
	}

	if len(slots) == 0 {
		h.send(ctx, b, chatID, "На этот день свободных слотов не осталось. Выберите другой день.", nil)
		h.showDays(ctx, b, chatID, serviceID)
		return
	}

	next := State{Step: StepChoosingSlot, ServiceID: serviceID, Day: dayRaw}
	if _, err := h.store.Advance(chatID, next); err != nil {
		h.rejectStaleButton(ctx, b, chatID, err)
		return
	}

	text := fmt.Sprintf("Свободное время на %s:", formatDayHuman(day, loc))
	h.send(ctx, b, chatID, text, slotsKeyboard(serviceID, slots, loc))
}

func (h *Handler) onSlotChosen(ctx context.Context, b *bot.Bot, chatID int64, cb Callback) {
	slotID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id слота", "error", err)
		return
	}

	slot, err := h.repo.SlotByID(ctx, slotID)
	if errors.Is(err, domain.ErrNotFound) {
		h.send(ctx, b, chatID, "Слот не найден. Начните заново: /start", nil)
		return
	}
	if err != nil {
		h.fail(ctx, b, chatID, "выборка слота", err)
		return
	}

	svc, err := h.repo.ServiceByID(ctx, slot.ServiceID)
	if err != nil {
		h.fail(ctx, b, chatID, "выборка услуги", err)
		return
	}

	current := h.store.Get(chatID)
	next := State{Step: StepConfirming, ServiceID: slot.ServiceID, Day: current.Day, SlotID: slotID}
	if _, err := h.store.Advance(chatID, next); err != nil {
		h.rejectStaleButton(ctx, b, chatID, err)
		return
	}

	loc := h.cfg.BusinessTZ
	text := fmt.Sprintf("Проверьте запись:\n\nУслуга: %s\nДата: %s\nВремя: %s—%s\nСтоимость: %d ₽",
		svc.Name, formatDayHuman(slot.Start, loc),
		formatTime(slot.Start, loc), formatTime(slot.End, loc), svc.Price)

	h.send(ctx, b, chatID, text, confirmKeyboard(slotID, slot.ServiceID))
}

func (h *Handler) onConfirm(ctx context.Context, b *bot.Bot, chatID, userID int64, cq *models.CallbackQuery, cb Callback) {
	slotID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id слота", "error", err)
		return
	}

	state := h.store.Get(chatID)
	if state.Step != StepConfirming || state.SlotID != slotID {
		h.send(ctx, b, chatID, "Эта кнопка устарела. Начните запись заново: /start", nil)
		return
	}

	booking, err := h.repo.CreateBooking(ctx, slotID, userID, clientName(cq.From))
	switch {
	case errors.Is(err, domain.ErrSlotTaken):
		// Состояние не сбрасываем: клиент остаётся в диалоге и выбирает другое время
		// той же услуги. Reset отправил бы его в StepIdle, откуда возврат к выбору дня запрещён.
		h.send(ctx, b, chatID, "Увы, этот слот только что заняли. Выберите другое время.", nil)
		h.showDays(ctx, b, chatID, state.ServiceID)
		return
	case err != nil:
		h.fail(ctx, b, chatID, "создание брони", err)
		return
	}

	h.store.Reset(chatID)
	h.log.Info("создана бронь", "booking_id", booking.ID, "slot_id", slotID, "client_tg_id", userID)

	loc := h.cfg.BusinessTZ
	h.send(ctx, b, chatID, formatBookingConfirmation(booking, loc), mainMenuKeyboard(h.cfg.IsAdmin(userID)))
	h.notifyAdmins(ctx, b, userID, formatAdminNotification(booking, loc))
}

func (h *Handler) showMyBookings(ctx context.Context, b *bot.Bot, chatID, userID int64) {
	bookings, err := h.repo.ActiveBookingsByClient(ctx, userID, h.now())
	if err != nil {
		h.fail(ctx, b, chatID, "выборка записей клиента", err)
		return
	}
	if len(bookings) == 0 {
		h.send(ctx, b, chatID, "У вас нет активных записей.", nil)
		return
	}

	loc := h.cfg.BusinessTZ
	lines := []string{"📋 Ваши записи:", ""}
	for _, bk := range bookings {
		lines = append(lines, fmt.Sprintf("#%d · %s · %s—%s · %s",
			bk.ID, formatDayHuman(bk.SlotStart, loc),
			formatTime(bk.SlotStart, loc), formatTime(bk.SlotEnd, loc), bk.ServiceName))
	}

	h.sendLong(ctx, b, chatID, lines, myBookingsKeyboard(bookings, loc))
}

func (h *Handler) onClientCancel(ctx context.Context, b *bot.Bot, chatID, userID int64, cb Callback) {
	bookingID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id брони", "error", err)
		return
	}

	booking, err := h.repo.CancelBooking(ctx, bookingID, userID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		h.send(ctx, b, chatID, "Запись не найдена.", nil)
		return
	case errors.Is(err, domain.ErrNotOwner):
		h.log.Warn("попытка отменить чужую запись", "booking_id", bookingID, "client_tg_id", userID)
		h.send(ctx, b, chatID, "Это не ваша запись.", nil)
		return
	case errors.Is(err, domain.ErrNotCancelable):
		h.send(ctx, b, chatID, "Запись уже отменена.", nil)
		return
	case err != nil:
		h.fail(ctx, b, chatID, "отмена брони", err)
		return
	}

	loc := h.cfg.BusinessTZ
	h.log.Info("бронь отменена клиентом", "booking_id", bookingID, "client_tg_id", userID)
	h.send(ctx, b, chatID, fmt.Sprintf("Запись #%d отменена. Слот снова свободен.", booking.ID), nil)

	h.notifyAdmins(ctx, b, userID, fmt.Sprintf("🔕 Клиент отменил запись #%d\n\n%s · %s",
		booking.ID, formatDayHuman(booking.SlotStart, loc), formatBookingLine(booking, loc)))
}

func (h *Handler) showAdminDayPicker(ctx context.Context, b *bot.Bot, chatID int64) {
	loc := h.cfg.BusinessTZ
	days := domain.UpcomingDays(h.now(), loc, bookingHorizonDays)
	h.send(ctx, b, chatID, "Выберите день:", adminDaysKeyboard(days, loc))
}

func (h *Handler) onAdminDay(ctx context.Context, b *bot.Bot, chatID, userID int64, cb Callback) {
	if !h.cfg.IsAdmin(userID) {
		h.log.Warn("не-админ дёрнул админский callback", "tg_id", userID)
		return
	}

	loc := h.cfg.BusinessTZ
	day, err := domain.ParseDay(cb.Arg(0), loc)
	if err != nil {
		h.log.Warn("некорректная дата в админском callback", "error", err, "day", cb.Arg(0))
		return
	}

	page := 0
	if raw := cb.Arg(1); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p >= 0 {
			page = p
		}
	}

	dayStart, dayEnd := domain.DayBounds(day, loc)
	bookings, err := h.repo.ActiveBookingsForDay(ctx, dayStart, dayEnd)
	if err != nil {
		h.fail(ctx, b, chatID, "выборка записей на день", err)
		return
	}
	if len(bookings) == 0 {
		h.send(ctx, b, chatID, fmt.Sprintf("На %s записей нет.", formatDayHuman(day, loc)), nil)
		return
	}

	pageItems, page, totalPages := paginate(bookings, page, adminPageSize)
	lines := buildDayReport(day, len(bookings), pageItems, loc)
	if totalPages > 1 {
		lines = append(lines, "", fmt.Sprintf("Страница %d из %d", page+1, totalPages))
	}

	h.sendLong(ctx, b, chatID, lines, adminPageKeyboard(pageItems, day, page, totalPages, loc))
}

func (h *Handler) onAdminCancel(ctx context.Context, b *bot.Bot, chatID, userID int64, cb Callback) {
	if !h.cfg.IsAdmin(userID) {
		h.log.Warn("не-админ пытался отменить запись", "tg_id", userID)
		return
	}

	bookingID, err := cb.IntArg(0)
	if err != nil {
		h.log.Warn("некорректный id брони", "error", err)
		return
	}

	// byTgID = 0: админ вправе отменить любую запись, проверка владельца не нужна.
	booking, err := h.repo.CancelBooking(ctx, bookingID, 0)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		h.send(ctx, b, chatID, "Запись не найдена.", nil)
		return
	case errors.Is(err, domain.ErrNotCancelable):
		h.send(ctx, b, chatID, "Запись уже отменена.", nil)
		return
	case err != nil:
		h.fail(ctx, b, chatID, "отмена брони админом", err)
		return
	}

	loc := h.cfg.BusinessTZ
	h.log.Info("бронь отменена админом", "booking_id", bookingID, "admin_tg_id", userID)
	h.send(ctx, b, chatID, fmt.Sprintf("Запись #%d отменена. Слот снова свободен.", booking.ID), nil)

	h.send(ctx, b, booking.ClientTgID, fmt.Sprintf(
		"❌ Ваша запись отменена администратором.\n\n%s · %s—%s · %s",
		formatDayHuman(booking.SlotStart, loc),
		formatTime(booking.SlotStart, loc), formatTime(booking.SlotEnd, loc), booking.ServiceName), nil)
}

// notifyAdmins рассылает уведомление админам, кроме exclude — чтобы админ,
// который сам записался или отменил свою запись, не получал уведомление о себе.
func (h *Handler) notifyAdmins(ctx context.Context, b *bot.Bot, exclude int64, text string) {
	for _, adminID := range h.cfg.AdminTgIDs {
		if adminID == exclude {
			continue
		}
		h.send(ctx, b, adminID, text, nil)
	}
}

func (h *Handler) send(ctx context.Context, b *bot.Bot, chatID int64, text string, markup models.ReplyMarkup) {
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: markup,
	}); err != nil {
		h.log.Error("отправка сообщения", "error", err, "chat_id", chatID)
	}
}

// sendLong режет длинный список на несколько сообщений: Telegram отклоняет
// сообщения длиннее 4096 символов, а список записей на день легко перерастает лимит.
// Клавиатура вешается на последнее сообщение.
func (h *Handler) sendLong(ctx context.Context, b *bot.Bot, chatID int64, lines []string, markup models.ReplyMarkup) {
	chunks := SplitMessage(lines, splitLimit)
	if len(chunks) == 0 {
		return
	}

	for i, chunk := range chunks {
		var m models.ReplyMarkup
		if i == len(chunks)-1 {
			m = markup
		}
		h.send(ctx, b, chatID, chunk, m)
	}
}

func (h *Handler) fail(ctx context.Context, b *bot.Bot, chatID int64, op string, err error) {
	h.log.Error(op, "error", err, "chat_id", chatID)
	h.send(ctx, b, chatID, "Не получилось выполнить действие. Попробуйте позже.", nil)
}

func (h *Handler) rejectStaleButton(ctx context.Context, b *bot.Bot, chatID int64, err error) {
	h.log.Debug("запрещённый переход FSM", "error", err, "chat_id", chatID)
	h.send(ctx, b, chatID, "Эта кнопка устарела. Начните заново: /start", nil)
}

func greeting(isAdmin bool) string {
	text := "Здравствуйте! Это бот записи.\n\nНажмите «📝 Записаться», чтобы выбрать услугу, день и время."
	if isAdmin {
		text += "\n\nВы вошли как администратор: доступна команда /day — записи на выбранный день."
	}
	return text
}

func clientName(u models.User) string {
	name := u.FirstName
	if u.LastName != "" {
		name += " " + u.LastName
	}
	if name == "" && u.Username != "" {
		name = "@" + u.Username
	}
	if name == "" {
		name = "Клиент " + strconv.FormatInt(u.ID, 10)
	}
	return name
}

func chatIDFromUpdate(update *models.Update) int64 {
	switch {
	case update.Message != nil:
		return update.Message.Chat.ID
	case update.CallbackQuery != nil && update.CallbackQuery.Message.Message != nil:
		return update.CallbackQuery.Message.Message.Chat.ID
	}
	return 0
}

func userIDFromMessage(msg *models.Message) int64 {
	if msg.From != nil {
		return msg.From.ID
	}
	return msg.Chat.ID
}

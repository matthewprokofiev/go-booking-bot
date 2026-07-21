package telegram

import (
	"fmt"
	"strconv"
	"strings"
)

// Действия инлайн-кнопок. Telegram ограничивает callback_data 64 байтами,
// поэтому префиксы короткие, а данные — только идентификаторы.
const (
	ActionService     = "svc"
	ActionDay         = "day"
	ActionSlot        = "slot"
	ActionConfirm     = "ok"
	ActionCancel      = "cxl"
	ActionAdminCancel = "acxl"
	ActionAdminDay    = "aday"
	ActionBackToSvc   = "bsvc"
	ActionBackToDay   = "bday"
	ActionMyBookings  = "my"
	ActionNoop        = "noop"
)

const callbackSep = ":"

type Callback struct {
	Action string
	Args   []string
}

func NewCallback(action string, args ...string) string {
	if len(args) == 0 {
		return action
	}
	return action + callbackSep + strings.Join(args, callbackSep)
}

func ParseCallback(data string) (Callback, error) {
	if data == "" {
		return Callback{}, fmt.Errorf("пустой callback_data")
	}

	parts := strings.Split(data, callbackSep)
	return Callback{Action: parts[0], Args: parts[1:]}, nil
}

func (c Callback) Arg(i int) string {
	if i >= len(c.Args) {
		return ""
	}
	return c.Args[i]
}

func (c Callback) IntArg(i int) (int64, error) {
	raw := c.Arg(i)
	if raw == "" {
		return 0, fmt.Errorf("аргумент %d отсутствует", i)
	}

	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("аргумент %d (%q) не число: %w", i, raw, err)
	}
	return v, nil
}

package telegram

import "strings"

// TelegramMaxMessageLen — жёсткий лимит Telegram на длину сообщения.
const TelegramMaxMessageLen = 4096

// splitLimit оставляет запас под служебные строки вроде «Страница 2/3».
const splitLimit = 3500

// SplitMessage режет длинный список на сообщения по границам строк, а не по символам:
// разрыв посреди строки в списке записей выглядит как баг. Строка длиннее лимита
// (теоретически возможна при очень длинном имени) режется принудительно — иначе
// Telegram отклонит сообщение целиком.
func SplitMessage(lines []string, limit int) []string {
	if limit <= 0 {
		limit = splitLimit
	}

	var (
		messages []string
		buf      strings.Builder
	)

	flush := func() {
		if buf.Len() > 0 {
			messages = append(messages, buf.String())
			buf.Reset()
		}
	}

	for _, line := range lines {
		for len(line) > limit {
			flush()
			cut := safeCut(line, limit)
			messages = append(messages, line[:cut])
			line = line[cut:]
		}

		extra := len(line)
		if buf.Len() > 0 {
			extra++ // перевод строки
		}
		if buf.Len()+extra > limit {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
	}
	flush()

	if len(messages) == 0 {
		return nil
	}
	return messages
}

// safeCut сдвигает точку реза влево до границы UTF-8, чтобы не разорвать
// многобайтовый символ пополам: кириллица в Telegram иначе превратится в мусор.
func safeCut(s string, limit int) int {
	cut := limit
	for cut > 0 && !isUTF8Start(s[cut]) {
		cut--
	}
	if cut == 0 {
		return limit
	}
	return cut
}

func isUTF8Start(b byte) bool {
	return b&0xC0 != 0x80
}

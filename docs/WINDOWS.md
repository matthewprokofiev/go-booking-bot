# Запуск на Windows

Гайд для Windows 10/11. Основной способ — через Docker Compose: он не требует ни Go, ни Postgres на машине. Раздел про локальный запуск через `go run` — для разработки.

Все команды даны для **PowerShell** (открывается через `Win + X` → «Терминал» или «Windows PowerShell»).

## Что нужно

| Требование | Проверка | Комментарий |
|---|---|---|
| Docker Desktop с бэкендом WSL2 | `docker version` | Должен быть **запущен** — иконка кита в трее не серая |
| Go 1.25+ | `go version` | Нужен только для локального запуска и тестов, для compose не требуется |
| Git | `git --version` | Чтобы склонировать репозиторий |

`make` в Windows нет по умолчанию — все цели `Makefile` продублированы обычными командами в разделе [Команды без make](#команды-без-make). Ставить `make` не обязательно.

---

## Шаг 1. Получить токен бота и свой Telegram ID

1. **Токен:** напишите [@BotFather](https://t.me/BotFather) → `/newbot` → придумайте имя и username → он пришлёт строку вида `123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`.
2. **Свой ID:** напишите [@userinfobot](https://t.me/userinfobot) → он пришлёт ваш числовой `Id`. Он нужен, чтобы получать уведомления о записях и команду `/day`.

## Шаг 2. Склонировать проект и создать `.env`

```powershell
git clone https://github.com/USERNAME/go-booking-bot.git
cd go-booking-bot
Copy-Item .env.example .env
```

Откройте `.env` и впишите свои значения:

```ini
BOT_TOKEN=123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
ADMIN_TG_IDS=123456789
```

Остальные переменные можно не трогать — у них рабочие значения по умолчанию.

> **Ловушка Windows:** не создавайте `.env` через «Блокнот» → «Сохранить как». Блокнот молча добавит расширение и получится `.env.txt`, а compose скажет `env file not found`. Используйте `Copy-Item` как выше, а редактируйте в VS Code или Notepad++.
>
> Проверить, что файл называется правильно: `Get-ChildItem -Force .env` — должен найтись ровно `.env`.

`.env` уже прописан в `.gitignore`, в репозиторий он не попадёт.

## Шаг 3. Запустить

```powershell
docker compose up -d --build
```

Первая сборка занимает пару минут (качается образ Go и зависимости), последующие — секунды.

Что произойдёт само, без ручных шагов: поднимется Postgres → Compose дождётся его healthcheck → стартует бот → применит миграции → засеет 4 демо-услуги и слоты на неделю вперёд в рабочих часах.

## Шаг 4. Проверить, что всё поднялось

```powershell
docker compose ps
docker compose logs -f app
```

В логах должно быть примерно это:

```
level=INFO msg="запуск бота" app_env=local business_tz=Europe/Moscow work_hours=10-20 admins=1
level=INFO msg="миграции применены"
level=INFO msg="демо-данные засеяны" services=4 slots=265 business_tz=Europe/Moscow work_hours=10-20
level=INFO msg="бот запущен, начинаю long polling"
```

Последняя строка — бот живой. Откройте своего бота в Telegram и отправьте `/start`.

Выйти из просмотра логов — `Ctrl + C` (бот продолжит работать в фоне).

## Остановка

```powershell
docker compose down      # остановить, данные Postgres сохранятся
docker compose down -v   # остановить и удалить данные (следующий старт заново засеет демо-данные)
```

---

## Команды без make

| Цель Makefile | Эквивалент в PowerShell |
|---|---|
| `make up` | `docker compose up -d --build` |
| `make down` | `docker compose down` |
| `make docker-build` | `docker compose build` |
| `make build` | `go build -o bin\bot.exe .\cmd\bot` |
| `make run` | `go run .\cmd\bot` (нужны переменные окружения, см. ниже) |
| `make test` | `go test ./...` (про `-race` см. [Тесты](#тесты)) |
| `make test-integration` | `go test -tags=integration -count=1 -timeout 600s ./...` |
| `make vet` | `go vet ./...` |
| `make lint` | `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...` |
| `make tidy` | `go mod tidy` |

Указывайте `bin\bot.exe` явно: без расширения `.exe` Windows не запустит собранный файл.

---

## Запуск без Docker (для разработки)

Приложение читает **только настоящие переменные окружения** — dotenv-загрузчика в проекте нет. Файл `.env` использует Docker Compose, а `go run` его не видит. Поэтому переменные нужно задать в сессии PowerShell вручную.

Postgres всё равно удобнее поднять в Docker:

```powershell
docker compose up -d postgres
```

> Compose требует `.env` даже для запуска одного только Postgres (файл упомянут в `docker-compose.yml`), так что Шаг 2 всё равно нужен.

Дальше — переменные и запуск. Обратите внимание: `DATABASE_URL` здесь указывает на `localhost`, а не на `postgres`, потому что бот работает вне docker-сети:

```powershell
$env:BOT_TOKEN     = "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
$env:DATABASE_URL  = "postgres://booking:booking@localhost:5432/booking?sslmode=disable"
$env:ADMIN_TG_IDS  = "123456789"
$env:BUSINESS_TZ   = "Europe/Moscow"
$env:APP_ENV       = "local"

go run .\cmd\bot
```

Переменные живут только в текущем окне PowerShell — в новом окне их нужно задать заново.

Остановить бота — `Ctrl + C`: он корректно закроет пул соединений и завершит polling.

---

## Тесты

```powershell
go test ./...
```

**Про `-race`:** цель `make test` использует детектор гонок, но на Windows он требует cgo и установленного C-компилятора (mingw-w64). Если увидите ошибку вида `-race requires cgo` или `gcc not found` — просто запускайте `go test ./...` без флага. Логика от этого проверяется та же, а гонки всё равно ловятся в CI на Linux.

**Интеграционные тесты** поднимают настоящий Postgres в testcontainers, поэтому Docker Desktop должен быть запущен:

```powershell
go test -tags=integration -count=1 -timeout 600s ./...
```

Первый прогон дольше — качается образ `postgres:16-alpine`. Контейнеры testcontainers удаляет за собой сам.

---

## Траблшутинг

**`env file ... .env not found`**
Не создан `.env` (Шаг 2) или он называется `.env.txt`. Проверьте: `Get-ChildItem -Force .env`.

**`error call getMe, not found, Not Found`**
Неверный `BOT_TOKEN`. Токен целиком, вместе с числами до двоеточия, без пробелов и кавычек внутри `.env`.

**`Ports are not available` / `bind: address already in use` на 5432**
На машине уже установлен Postgres и занял порт. Поменяйте в `.env`:
```ini
POSTGRES_PORT=5433
```
Внутри docker-сети бот ходит на 5432 напрямую, так что менять `DATABASE_URL` для compose не нужно. А вот для локального `go run` укажите новый порт: `...@localhost:5433/...`.

**`cannot connect to the Docker daemon` / `docker: error during connect`**
Docker Desktop не запущен. Запустите его и дождитесь, пока иконка кита в трее перестанет мигать.

**Бот стартует, но сразу перезапускается**
Смотрите причину: `docker compose logs app`. Чаще всего это неверный токен или незаполненный `.env`.

**Бот в Telegram молчит на `/start`**
Убедитесь, что в логах есть `бот запущен, начинаю long polling`, и что вы пишете именно тому боту, чей токен в `.env` (username из ответа BotFather).

**Время слотов выглядит неправильным**
Бот показывает время в таймзоне бизнеса из `BUSINESS_TZ`, а не в таймзоне вашей Windows. По умолчанию это `Europe/Moscow`. Поменяйте на свою (`Europe/Kaliningrad`, `Asia/Yekaterinburg` и т.д.) и пересоздайте данные: `docker compose down -v`, затем `docker compose up -d`.

**Изменил `.env`, ничего не поменялось**
Compose читает `.env` при старте контейнера: `docker compose up -d` перечитает его. Если меняли `WORK_HOURS_*` или `BUSINESS_TZ`, слоты уже засеяны в старых часах — нужен `docker compose down -v` для пересева.

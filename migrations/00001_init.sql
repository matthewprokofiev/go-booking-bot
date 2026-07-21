-- +goose Up
CREATE TABLE services (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT    NOT NULL,
    duration_min INT     NOT NULL CHECK (duration_min > 0),
    price        INT     NOT NULL CHECK (price >= 0),
    is_active    BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE schedule_slots (
    id         BIGSERIAL PRIMARY KEY,
    service_id BIGINT      NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    slot_start TIMESTAMPTZ NOT NULL,
    slot_end   TIMESTAMPTZ NOT NULL,
    is_booked  BOOLEAN     NOT NULL DEFAULT FALSE,
    CONSTRAINT slot_end_after_start CHECK (slot_end > slot_start),
    CONSTRAINT uniq_service_slot_start UNIQUE (service_id, slot_start)
);

CREATE INDEX idx_slots_start ON schedule_slots (slot_start);

-- Выборка свободных слотов на день: фильтр по услуге и диапазону дня среди незанятых.
CREATE INDEX idx_slots_free_lookup ON schedule_slots (service_id, slot_start) WHERE is_booked = FALSE;

CREATE TABLE bookings (
    id            BIGSERIAL PRIMARY KEY,
    slot_id       BIGINT      NOT NULL REFERENCES schedule_slots (id) ON DELETE CASCADE,
    client_tg_id  BIGINT      NOT NULL,
    client_name   TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Колоночный UNIQUE(slot_id) здесь запрещён: отменённые брони остаются в таблице,
-- и слот обязан принимать новую активную бронь. Уникальна только активная бронь на слот.
CREATE UNIQUE INDEX uniq_active_booking ON bookings (slot_id) WHERE status = 'active';

CREATE INDEX idx_bookings_client_active ON bookings (client_tg_id) WHERE status = 'active';

CREATE TABLE admins (
    id    BIGSERIAL PRIMARY KEY,
    tg_id BIGINT NOT NULL UNIQUE,
    name  TEXT   NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS admins;
DROP TABLE IF EXISTS bookings;
DROP TABLE IF EXISTS schedule_slots;
DROP TABLE IF EXISTS services;

package domain

import (
	"errors"
	"time"
)

const (
	StatusActive    = "active"
	StatusCancelled = "cancelled"
)

var (
	ErrSlotTaken     = errors.New("слот уже занят")
	ErrNotFound      = errors.New("не найдено")
	ErrNotOwner      = errors.New("запись принадлежит другому клиенту")
	ErrNotCancelable = errors.New("запись нельзя отменить")
)

type Service struct {
	ID          int64
	Name        string
	DurationMin int
	Price       int
	IsActive    bool
}

type Slot struct {
	ID        int64
	ServiceID int64
	Start     time.Time
	End       time.Time
	IsBooked  bool
}

type Booking struct {
	ID          int64
	SlotID      int64
	ClientTgID  int64
	ClientName  string
	Status      string
	CreatedAt   time.Time
	ServiceName string
	SlotStart   time.Time
	SlotEnd     time.Time
	DurationMin int
	Price       int
}

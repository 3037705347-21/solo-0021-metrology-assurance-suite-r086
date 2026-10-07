package domain

import (
	"fmt"
	"strings"
	"time"
)

type SuspensionStatus string

const (
	SuspensionActive   SuspensionStatus = "active"
	SuspensionReleased SuspensionStatus = "released"
	SuspensionExpired  SuspensionStatus = "expired"
)

const maxSuspensionReasonLength = 512

// MaintenanceSuspension is a time-bounded maintenance fact attached to a
// device. While active, no new verification case can be opened for the
// device. It is a fact layered beside the device lifecycle: an active device
// stays active while it is suspended. Every suspension is retained, so the
// history of pauses remains readable after release or expiry.
type MaintenanceSuspension struct {
	ID          string           `json:"id"`
	DeviceID    string           `json:"device_id"`
	Reason      string           `json:"reason"`
	Actor       string           `json:"actor"`
	Status      SuspensionStatus `json:"status"`
	StartedAt   time.Time        `json:"started_at"`
	ExpiresAt   time.Time        `json:"expires_at"`
	EndedAt     *time.Time       `json:"ended_at,omitempty"`
	EndedReason string           `json:"ended_reason,omitempty"`
}

func NewMaintenanceSuspension(id, deviceID, reason, actor string, start, expiry time.Time) (MaintenanceSuspension, error) {
	item := MaintenanceSuspension{
		ID:        strings.TrimSpace(id),
		DeviceID:  strings.TrimSpace(deviceID),
		Reason:    strings.TrimSpace(reason),
		Actor:     strings.TrimSpace(actor),
		Status:    SuspensionActive,
		StartedAt: start.UTC(),
		ExpiresAt: expiry.UTC(),
	}
	if err := item.Validate(); err != nil {
		return MaintenanceSuspension{}, err
	}
	return item, nil
}

func (s MaintenanceSuspension) Validate() error {
	if s.ID == "" || s.DeviceID == "" || s.Reason == "" || s.Actor == "" {
		return fmt.Errorf("%w: suspension id, device id, reason, and actor are required", ErrInvalidInput)
	}
	if len(s.Reason) > maxSuspensionReasonLength {
		return fmt.Errorf("%w: suspension reason is too long", ErrInvalidInput)
	}
	if s.StartedAt.IsZero() || s.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: suspension start and expiry times are required", ErrInvalidInput)
	}
	if !s.ExpiresAt.After(s.StartedAt) {
		return fmt.Errorf("%w: suspension expiry must be after the start time", ErrInvalidInput)
	}
	switch s.Status {
	case SuspensionActive, SuspensionReleased, SuspensionExpired:
		return nil
	default:
		return fmt.Errorf("%w: unsupported suspension status %q", ErrInvalidInput, s.Status)
	}
}

// IsActiveAt reports whether the suspension is still blocking at the given
// observation time. Expiry is evaluated against the declared time limit so a
// suspension past its limit is never treated as active, even before its
// status has been materialized.
func (s MaintenanceSuspension) IsActiveAt(now time.Time) bool {
	return s.Status == SuspensionActive && now.UTC().Before(s.ExpiresAt)
}

// Release ends an active suspension manually ahead of its expiry limit.
func (s *MaintenanceSuspension) Release(at time.Time, note string) error {
	if s.Status != SuspensionActive {
		return fmt.Errorf("%w: suspension %s is %s and cannot be released", ErrStateConflict, s.ID, s.Status)
	}
	stamp := at.UTC()
	s.Status = SuspensionReleased
	s.EndedAt = &stamp
	s.EndedReason = strings.TrimSpace(note)
	return nil
}

// Expire ends an active suspension that has reached its declared limit.
func (s *MaintenanceSuspension) Expire(at time.Time) error {
	if s.Status != SuspensionActive {
		return fmt.Errorf("%w: suspension %s is %s and cannot expire", ErrStateConflict, s.ID, s.Status)
	}
	if at.UTC().Before(s.ExpiresAt) {
		return fmt.Errorf("%w: suspension %s has not reached its expiry time", ErrStateConflict, s.ID)
	}
	stamp := at.UTC()
	s.Status = SuspensionExpired
	s.EndedAt = &stamp
	s.EndedReason = "suspension reached its declared expiry time"
	return nil
}

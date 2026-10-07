package domain

import (
	"fmt"
	"strings"
	"time"
)

type PauseStatus string

const (
	PauseActive   PauseStatus = "active"
	PauseReleased PauseStatus = "released"
	PauseExpired  PauseStatus = "expired"
)

const (
	MaxPauseReasonLength = 512
	MaxPauseActorLength  = 128
	// MaxPauseDuration bounds a maintenance window to short-term downtime.
	MaxPauseDuration = 7 * 24 * time.Hour
)

// MaintenancePause is the formal fact that a device is stopped for
// maintenance or on-site inspection. Pause records are never deleted: a
// released or expired pause stays readable as history.
type MaintenancePause struct {
	ID          string      `json:"id"`
	DeviceID    string      `json:"device_id"`
	Reason      string      `json:"reason"`
	RequestedBy string      `json:"requested_by"`
	Status      PauseStatus `json:"status"`
	StartedAt   time.Time   `json:"started_at"`
	ExpiresAt   time.Time   `json:"expires_at"`
	EndedAt     *time.Time  `json:"ended_at,omitempty"`
	ReleasedBy  string      `json:"released_by,omitempty"`
}

func NewMaintenancePause(id, deviceID, reason, requestedBy string, startedAt, expiresAt time.Time) (MaintenancePause, error) {
	item := MaintenancePause{
		ID:          strings.TrimSpace(id),
		DeviceID:    strings.TrimSpace(deviceID),
		Reason:      strings.TrimSpace(reason),
		RequestedBy: strings.TrimSpace(requestedBy),
		Status:      PauseActive,
		StartedAt:   startedAt.UTC(),
		ExpiresAt:   expiresAt.UTC(),
	}
	if err := item.Validate(); err != nil {
		return MaintenancePause{}, err
	}
	return item, nil
}

func (p MaintenancePause) Validate() error {
	if p.ID == "" || p.DeviceID == "" {
		return fmt.Errorf("%w: pause id and device id are required", ErrInvalidInput)
	}
	if p.Reason == "" {
		return fmt.Errorf("%w: maintenance reason is required", ErrInvalidInput)
	}
	if len(p.Reason) > MaxPauseReasonLength {
		return fmt.Errorf("%w: maintenance reason is too long", ErrInvalidInput)
	}
	if p.RequestedBy == "" {
		return fmt.Errorf("%w: requesting steward is required", ErrInvalidInput)
	}
	if len(p.RequestedBy) > MaxPauseActorLength {
		return fmt.Errorf("%w: requesting steward name is too long", ErrInvalidInput)
	}
	if p.StartedAt.IsZero() || p.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: pause start and expiry times are required", ErrInvalidInput)
	}
	if !p.ExpiresAt.After(p.StartedAt) {
		return fmt.Errorf("%w: pause expiry must be after the start time", ErrInvalidInput)
	}
	switch p.Status {
	case PauseActive, PauseReleased, PauseExpired:
	default:
		return fmt.Errorf("%w: unsupported pause status %q", ErrInvalidInput, p.Status)
	}
	if p.Status == PauseActive {
		if p.EndedAt != nil {
			return fmt.Errorf("%w: an active pause cannot carry an end time", ErrInvalidInput)
		}
		if p.ReleasedBy != "" {
			return fmt.Errorf("%w: an active pause cannot record a releasing steward", ErrInvalidInput)
		}
	}
	if p.Status != PauseActive && p.EndedAt == nil {
		return fmt.Errorf("%w: an ended pause requires an end time", ErrInvalidInput)
	}
	return nil
}

// IsDue reports whether an active pause has reached its scheduled expiry.
func (p MaintenancePause) IsDue(at time.Time) bool {
	return p.Status == PauseActive && !at.UTC().Before(p.ExpiresAt)
}

// Expire settles an active pause that has reached its expiry time. The end
// time is the scheduled expiry instant, which is the factual end of the pause.
func (p *MaintenancePause) Expire(at time.Time) error {
	if p.Status != PauseActive {
		return fmt.Errorf("%w: only an active pause can expire", ErrPauseNotActive)
	}
	if at.UTC().Before(p.ExpiresAt) {
		return fmt.Errorf("%w: pause has not reached its expiry time", ErrStateConflict)
	}
	end := p.ExpiresAt
	p.Status = PauseExpired
	p.EndedAt = &end
	return nil
}

// Release ends an active pause early at the request of a steward. A pause that
// has already reached its expiry time cannot be released: expiry is the single
// terminal outcome for that window.
func (p *MaintenancePause) Release(at time.Time, by string) error {
	actor := strings.TrimSpace(by)
	if actor == "" {
		return fmt.Errorf("%w: releasing steward is required", ErrInvalidInput)
	}
	if len(actor) > MaxPauseActorLength {
		return fmt.Errorf("%w: releasing steward name is too long", ErrInvalidInput)
	}
	if p.Status != PauseActive {
		return fmt.Errorf("%w: pause %s is %s", ErrPauseNotActive, p.ID, p.Status)
	}
	if !at.UTC().Before(p.ExpiresAt) {
		return fmt.Errorf("%w: pause %s has already expired", ErrPauseNotActive, p.ID)
	}
	stamp := at.UTC()
	p.Status = PauseReleased
	p.EndedAt = &stamp
	p.ReleasedBy = actor
	return nil
}

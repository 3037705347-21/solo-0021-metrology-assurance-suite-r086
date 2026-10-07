package domain

import (
	"errors"
	"testing"
	"time"
)

func TestMaintenancePauseLifecycle(t *testing.T) {
	base := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

	t.Run("requires reason, actor and a later expiry", func(t *testing.T) {
		cases := []struct {
			name     string
			reason   string
			actor    string
			expiry   time.Time
			expected error
		}{
			{"empty reason", " ", "Lin", base.Add(time.Hour), ErrInvalidInput},
			{"empty actor", "calibration", " ", base.Add(time.Hour), ErrInvalidInput},
			{"expiry not after start", "calibration", "Lin", base, ErrInvalidInput},
			{"expiry before start", "calibration", "Lin", base.Add(-time.Minute), ErrInvalidInput},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := NewMaintenancePause("pause-1", "device-1", tc.reason, tc.actor, base, tc.expiry)
				if !errors.Is(err, tc.expected) {
					t.Fatalf("error = %v, want %v", err, tc.expected)
				}
			})
		}
	})

	t.Run("active pause blocks until expiry", func(t *testing.T) {
		pause, err := NewMaintenancePause("pause-1", "device-1", "bench service", "Lin", base, base.Add(30*time.Minute))
		if err != nil {
			t.Fatalf("new pause: %v", err)
		}
		if pause.Status != PauseActive {
			t.Fatalf("status = %s", pause.Status)
		}
		if pause.IsDue(base.Add(29 * time.Minute)) {
			t.Fatal("pause reported due before its expiry")
		}
		if !pause.IsDue(base.Add(30 * time.Minute)) {
			t.Fatal("pause not due exactly at its expiry")
		}
	})

	t.Run("release before expiry ends the pause once", func(t *testing.T) {
		pause, _ := NewMaintenancePause("pause-1", "device-1", "bench service", "Lin", base, base.Add(30*time.Minute))
		if err := pause.Release(base.Add(10*time.Minute), "Mira"); err != nil {
			t.Fatalf("release: %v", err)
		}
		if pause.Status != PauseReleased || pause.EndedAt == nil || pause.ReleasedBy != "Mira" {
			t.Fatalf("released pause = %+v", pause)
		}
		if err := pause.Release(base.Add(11*time.Minute), "Mira"); !errors.Is(err, ErrPauseNotActive) {
			t.Fatalf("second release error = %v, want %v", err, ErrPauseNotActive)
		}
		if err := pause.Expire(base.Add(31 * time.Minute)); !errors.Is(err, ErrPauseNotActive) {
			t.Fatalf("expire after release error = %v, want %v", err, ErrPauseNotActive)
		}
	})

	t.Run("release after the expiry instant is refused", func(t *testing.T) {
		pause, _ := NewMaintenancePause("pause-1", "device-1", "bench service", "Lin", base, base.Add(30*time.Minute))
		err := pause.Release(base.Add(30*time.Minute), "Lin")
		if !errors.Is(err, ErrPauseNotActive) {
			t.Fatalf("late release error = %v, want %v", err, ErrPauseNotActive)
		}
		if pause.Status != PauseActive {
			t.Fatal("a refused release must not change the pause status")
		}
	})

	t.Run("expire ends at the scheduled instant once", func(t *testing.T) {
		pause, _ := NewMaintenancePause("pause-1", "device-1", "bench service", "Lin", base, base.Add(30*time.Minute))
		if err := pause.Expire(base.Add(35 * time.Minute)); err != nil {
			t.Fatalf("expire: %v", err)
		}
		if pause.Status != PauseExpired {
			t.Fatalf("status = %s", pause.Status)
		}
		if pause.EndedAt == nil || !pause.EndedAt.Equal(base.Add(30*time.Minute)) {
			t.Fatalf("ended at %v, want the scheduled expiry instant", pause.EndedAt)
		}
		if err := pause.Expire(base.Add(40 * time.Minute)); !errors.Is(err, ErrPauseNotActive) {
			t.Fatalf("second expire error = %v, want %v", err, ErrPauseNotActive)
		}
	})

	t.Run("release requires a named steward", func(t *testing.T) {
		pause, _ := NewMaintenancePause("pause-1", "device-1", "bench service", "Lin", base, base.Add(30*time.Minute))
		if err := pause.Release(base.Add(5*time.Minute), "  "); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("blank release error = %v, want %v", err, ErrInvalidInput)
		}
	})
}

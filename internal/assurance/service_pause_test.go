package assurance

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type controllableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *controllableClock {
	return &controllableClock{now: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
}

func (c *controllableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *controllableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestService(t *testing.T) (*Service, *controllableClock) {
	t.Helper()
	clock := newClock()
	return NewServiceWithClock(repository.New(), clock.Now), clock
}

func registerDevice(t *testing.T, service *Service, tag string) domain.Device {
	t.Helper()
	view, err := service.RegisterDevice(context.Background(), contracts.RegisterDeviceRequest{
		AssetTag: tag,
		Model:    "Bench Meter",
		Room:     "Room A",
		Steward:  "Lin",
	})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	return view.Device
}

func pauseDevice(t *testing.T, service *Service, deviceID string, minutes int) domain.MaintenancePause {
	t.Helper()
	view, err := service.StartMaintenancePause(context.Background(), deviceID, contracts.StartPauseRequest{
		Reason:          "scheduled calibration bench maintenance",
		RequestedBy:     "Lin",
		DurationMinutes: minutes,
	})
	if err != nil {
		t.Fatalf("start pause: %v", err)
	}
	return view.Pause
}

func TestPauseBlocksOpenCaseAndAdmitsAfterRelease(t *testing.T) {
	service, clock := newTestService(t)
	device := registerDevice(t, service, "MET-2001")
	pause := pauseDevice(t, service, device.ID, 30)

	if _, err := service.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: device.ID, Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
	}); !errors.Is(err, domain.ErrPauseAlreadyActive) {
		t.Fatalf("open while paused error = %v, want %v", err, domain.ErrPauseAlreadyActive)
	}

	events, err := service.DeviceEvents(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("device events: %v", err)
	}
	if !containsKind(events.Events, "case_open_blocked") {
		t.Fatal("blocked open attempt was not audited")
	}

	if _, err := service.ReleaseMaintenancePause(context.Background(), pause.ID, contracts.ReleasePauseRequest{
		ReleasedBy: "Lin",
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	opened, err := service.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: device.ID, Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("open after release: %v", err)
	}
	if opened.Case.State != domain.CaseAwaitingMeasurement {
		t.Fatalf("case state = %s", opened.Case.State)
	}

	deviceView, err := service.Device(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("device view: %v", err)
	}
	if deviceView.CurrentPause != nil {
		t.Fatalf("device still reports pause %s after release", deviceView.CurrentPause.ID)
	}

	_ = clock
}

func TestRepeatedPauseRejectedAndHistoryPreserved(t *testing.T) {
	service, _ := newTestService(t)
	device := registerDevice(t, service, "MET-2002")
	first := pauseDevice(t, service, device.ID, 30)

	if _, err := service.StartMaintenancePause(context.Background(), device.ID, contracts.StartPauseRequest{
		Reason: "second stop", RequestedBy: "Mira", DurationMinutes: 45,
	}); !errors.Is(err, domain.ErrPauseAlreadyActive) {
		t.Fatalf("duplicate pause error = %v, want %v", err, domain.ErrPauseAlreadyActive)
	}

	if _, err := service.ReleaseMaintenancePause(context.Background(), first.ID, contracts.ReleasePauseRequest{
		ReleasedBy: "Lin",
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	second := pauseDevice(t, service, device.ID, 30)

	history, err := service.DevicePauses(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("pause history: %v", err)
	}
	if len(history.Pauses) != 2 {
		t.Fatalf("history length = %d, want 2", len(history.Pauses))
	}
	if history.Pauses[0].ID != first.ID || history.Pauses[0].Status != domain.PauseReleased {
		t.Fatalf("first history entry = %+v", history.Pauses[0])
	}
	if history.Pauses[1].ID != second.ID || history.Pauses[1].Status != domain.PauseActive {
		t.Fatalf("second history entry = %+v", history.Pauses[1])
	}
}

func TestPauseAutoExpiryIsSingleOutcome(t *testing.T) {
	service, clock := newTestService(t)
	device := registerDevice(t, service, "MET-2003")
	pause := pauseDevice(t, service, device.ID, 10)

	clock.Advance(11 * time.Minute)

	// A late release cannot win over automatic expiry, but the expiry event
	// still commits.
	if _, err := service.ReleaseMaintenancePause(context.Background(), pause.ID, contracts.ReleasePauseRequest{
		ReleasedBy: "Lin",
	}); !errors.Is(err, domain.ErrPauseNotActive) {
		t.Fatalf("late release error = %v, want %v", err, domain.ErrPauseNotActive)
	}

	view, err := service.Pause(context.Background(), pause.ID)
	if err != nil {
		t.Fatalf("pause view: %v", err)
	}
	if view.Pause.Status != domain.PauseExpired || view.Pause.EndedAt == nil {
		t.Fatalf("pause = %+v, want a settled expiry", view.Pause)
	}

	opened, err := service.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: device.ID, Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("open after expiry: %v", err)
	}
	if opened.Case.DeviceID != device.ID {
		t.Fatal("admitted case is bound to the wrong device")
	}

	events, err := service.DeviceEvents(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("device events: %v", err)
	}
	if countKind(events.Events, "maintenance_pause_expired") != 1 {
		t.Fatal("automatic expiry was not recorded exactly once")
	}
}

func TestConcurrentPauseAndOpenYieldOneResult(t *testing.T) {
	service, _ := newTestService(t)
	device := registerDevice(t, service, "MET-2004")

	type outcome struct {
		pauseCreated bool
		caseCreated  bool
		blocked      bool
	}
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup

	wait.Add(1)
	go func() {
		defer wait.Done()
		_, err := service.StartMaintenancePause(context.Background(), device.ID, contracts.StartPauseRequest{
			Reason: "race", RequestedBy: "Lin", DurationMinutes: 30,
		})
		outcomes <- outcome{pauseCreated: err == nil}
	}()
	wait.Add(1)
	go func() {
		defer wait.Done()
		_, err := service.OpenCase(context.Background(), contracts.OpenCaseRequest{
			DeviceID: device.ID, Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
		})
		switch {
		case err == nil:
			outcomes <- outcome{caseCreated: true}
		case errors.Is(err, domain.ErrPauseAlreadyActive):
			outcomes <- outcome{blocked: true}
		default:
			t.Errorf("concurrent open error = %v", err)
			outcomes <- outcome{}
		}
	}()
	wait.Wait()
	close(outcomes)

	pauses, cases, blocked := 0, 0, 0
	for item := range outcomes {
		if item.pauseCreated {
			pauses++
		}
		if item.caseCreated {
			cases++
		}
		if item.blocked {
			blocked++
		}
	}
	if pauses != 1 {
		t.Fatalf("created pauses = %d, want 1", pauses)
	}
	if !(cases == 1 && blocked == 0) && !(cases == 0 && blocked == 1) {
		t.Fatalf("ambiguous race: %d cases and %d blocked opens", cases, blocked)
	}
}

func TestPauseWindowValidation(t *testing.T) {
	service, _ := newTestService(t)
	device := registerDevice(t, service, "MET-2005")
	base := service.now()

	cases := []struct {
		name  string
		input contracts.StartPauseRequest
	}{
		{"no window", contracts.StartPauseRequest{Reason: "x", RequestedBy: "Lin"}},
		{"both windows", contracts.StartPauseRequest{
			Reason: "x", RequestedBy: "Lin", DurationMinutes: 30,
			ExpiresAt: timePointer(base.Add(time.Hour)),
		}},
		{"zero duration", contracts.StartPauseRequest{Reason: "x", RequestedBy: "Lin", DurationMinutes: 0, ExpiresAt: nil}},
		{"negative duration", contracts.StartPauseRequest{Reason: "x", RequestedBy: "Lin", DurationMinutes: -5}},
		{"past expiry", contracts.StartPauseRequest{Reason: "x", RequestedBy: "Lin", ExpiresAt: timePointer(base.Add(-time.Minute))}},
		{"window too long", contracts.StartPauseRequest{Reason: "x", RequestedBy: "Lin", DurationMinutes: int(domain.MaxPauseDuration/time.Minute) + 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.StartMaintenancePause(context.Background(), device.ID, tc.input); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("error = %v, want %v", err, domain.ErrInvalidInput)
			}
		})
	}

	history, err := service.DevicePauses(context.Background(), device.ID)
	if err != nil {
		t.Fatalf("pause history: %v", err)
	}
	if len(history.Pauses) != 0 {
		t.Fatalf("invalid requests created %d pauses", len(history.Pauses))
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func containsKind(items []domain.AuditEvent, kind string) bool {
	return countKind(items, kind) > 0
}

func countKind(items []domain.AuditEvent, kind string) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

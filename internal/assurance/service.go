package assurance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type Service struct {
	repository *repository.Repository
	now        func() time.Time
}

func NewService(repo *repository.Repository) *Service {
	return &Service{
		repository: repo,
		now:        time.Now,
	}
}

// NewServiceWithClock wires a fixed or controllable clock. It exists for
// bounded verification of time-based pause expiry.
func NewServiceWithClock(repo *repository.Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		repository: repo,
		now:        now,
	}
}

func (s *Service) RegisterDevice(ctx context.Context, input contracts.RegisterDeviceRequest) (contracts.DeviceResponse, error) {
	var created domain.Device
	tag := domain.NormalizeAssetTag(input.AssetTag)
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		if _, exists := m.DeviceByTag(tag); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateAssetTag, tag)
		}
		item, err := domain.NewDevice(
			m.NextID("device"),
			input.AssetTag,
			input.Model,
			input.Room,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutDevice(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			item.ID,
			"",
			"device_enrolled",
			item.Steward,
			"device enrolled for assurance work",
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	if err != nil {
		return contracts.DeviceResponse{}, err
	}
	return contracts.DeviceResponse{Device: created}, nil
}

func (s *Service) Device(ctx context.Context, id string) (contracts.DeviceResponse, error) {
	var result contracts.DeviceResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Device(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		result = deviceResponseFromSnapshot(snapshot, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) OpenCase(ctx context.Context, input contracts.OpenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	// A blocked attempt is itself a fact worth keeping: its audit event must
	// commit, so the rejection is surfaced after the mutation succeeds.
	var blocked error
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		now := s.now()
		deviceID := strings.TrimSpace(input.DeviceID)
		device, exists := m.Device(deviceID)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, deviceID)
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		// Settle any due pause inside this same commit so expiry and the
		// admission decision share one atomic fact.
		if _, _, err := s.settleDevicePause(m, device.ID, now); err != nil {
			return err
		}
		if pause, isBlocked := effectivePause(m, device.ID, now); isBlocked {
			if err := s.recordBlockedOpen(m, pause, input.Steward, now); err != nil {
				return err
			}
			blocked = fmt.Errorf("%w: new cases for device %s are blocked until %s by pause %s",
				domain.ErrPauseAlreadyActive, device.ID, pause.ExpiresAt.Format(time.RFC3339), pause.ID)
			return nil
		}
		if active, exists := m.ActiveCaseForDevice(device.ID); exists {
			return fmt.Errorf("%w: case %s is still %s", domain.ErrActiveCaseExists, active.ID, active.State)
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			input.Nominal,
			input.Tolerance,
			input.Steward,
			now,
		)
		if err != nil {
			return err
		}
		if err := m.PutCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_opened",
			item.Steward,
			"verification case opened",
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	if err != nil {
		return contracts.CaseResponse{}, err
	}
	if blocked != nil {
		return contracts.CaseResponse{}, blocked
	}
	return result, nil
}

func (s *Service) Case(ctx context.Context, id string) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Case(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		result = caseResponseFromSnapshot(snapshot, item)
		return nil
	})
	return result, err
}

func (s *Service) RecordMeasurement(ctx context.Context, caseID string, input contracts.RecordMeasurementRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		now := s.now()
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		measurement, err := domain.NewMeasurement(
			m.NextID("measurement"),
			item.ID,
			input.Observed,
			item.Nominal,
			item.Tolerance,
			input.Technician,
			now,
		)
		if err != nil {
			return err
		}
		if err := item.RecordMeasurement(measurement.Accepted, now); err != nil {
			return err
		}
		if err := m.AppendMeasurement(measurement); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		kind := "measurement_rejected"
		detail := "measurement exceeded tolerance and requires another measurement"
		if measurement.Accepted {
			kind = "measurement_accepted"
			detail = "measurement is within tolerance and ready for review"
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			kind,
			measurement.Technician,
			detail,
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

func (s *Service) ReopenCase(ctx context.Context, caseID string, input contracts.ReopenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		now := s.now()
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if err := item.Reopen(now); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		reason := strings.TrimSpace(input.Reason)
		if actor == "" || reason == "" {
			return fmt.Errorf("%w: actor and reason are required", domain.ErrInvalidInput)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_reopened",
			actor,
			reason,
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

func (s *Service) SealCase(ctx context.Context, caseID string, input contracts.SealCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		now := s.now()
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if _, exists := m.Seal(item.ID); exists {
			return fmt.Errorf("%w: case %s", domain.ErrSealAlreadyCreated, item.ID)
		}
		measurement, ok := bestAcceptedMeasurement(m.Measurements(item.ID))
		if !ok {
			return fmt.Errorf("%w: an accepted measurement is required before sealing", domain.ErrStateConflict)
		}
		if err := item.Seal(now); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(
			m.NextID("seal"),
			item.ID,
			item.DeviceID,
			measurement.ID,
			input.Reviewer,
			input.Summary,
			now,
		)
		if err != nil {
			return err
		}
		if err := m.PutSeal(seal); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_sealed",
			seal.Reviewer,
			seal.Summary,
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

// StartMaintenancePause opens the formal downtime fact for a device. Only an
// active device without an effective (non-expired) active pause can be paused.
func (s *Service) StartMaintenancePause(ctx context.Context, deviceID string, input contracts.StartPauseRequest) (contracts.PauseResponse, error) {
	// Validate the time-bounded request before touching device state so a
	// malformed window never produces a state conflict.
	now := s.now()
	if _, err := resolvePauseExpiry(input, now); err != nil {
		return contracts.PauseResponse{}, err
	}
	var created domain.MaintenancePause
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(deviceID)
		device, exists := m.Device(id)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		if _, _, err := s.settleDevicePause(m, device.ID, now); err != nil {
			return err
		}
		if pause, blocked := effectivePause(m, device.ID, now); blocked {
			return fmt.Errorf("%w: pause %s is effective until %s",
				domain.ErrPauseAlreadyActive, pause.ID, pause.ExpiresAt.Format(time.RFC3339))
		}
		expiresAt, err := resolvePauseExpiry(input, now)
		if err != nil {
			return err
		}
		item, err := domain.NewMaintenancePause(
			m.NextID("pause"),
			device.ID,
			input.Reason,
			input.RequestedBy,
			now,
			expiresAt,
		)
		if err != nil {
			return err
		}
		if err := m.PutPause(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			device.ID,
			"",
			"maintenance_pause_started",
			item.RequestedBy,
			fmt.Sprintf("maintenance pause %s started until %s: %s",
				item.ID, item.ExpiresAt.Format(time.RFC3339), item.Reason),
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	if err != nil {
		return contracts.PauseResponse{}, err
	}
	return contracts.PauseResponse{Pause: created}, nil
}

// ReleaseMaintenancePause ends a pause early. A pause that has reached its
// scheduled expiry is expired instead and cannot be released.
func (s *Service) ReleaseMaintenancePause(ctx context.Context, pauseID string, input contracts.ReleasePauseRequest) (contracts.PauseResponse, error) {
	var result contracts.PauseResponse
	// When the window has already elapsed, expiry must still be committed;
	// the conflict is returned after that commit.
	var expired error
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		now := s.now()
		id := strings.TrimSpace(pauseID)
		item, exists := m.Pause(id)
		if !exists {
			return fmt.Errorf("%w: maintenance pause %s", domain.ErrNotFound, id)
		}
		// Settle expiry first: expiry beats a late release and is the single
		// terminal outcome for the window.
		if item.IsDue(now) {
			if _, _, err := s.settleDevicePause(m, item.DeviceID, now); err != nil {
				return err
			}
			item, _ = m.Pause(id)
			expired = fmt.Errorf("%w: pause %s has expired at %s",
				domain.ErrPauseNotActive, item.ID, item.ExpiresAt.Format(time.RFC3339))
			return nil
		}
		if err := item.Release(now, input.ReleasedBy); err != nil {
			return err
		}
		if err := m.UpdatePause(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			item.DeviceID,
			"",
			"maintenance_pause_released",
			item.ReleasedBy,
			fmt.Sprintf("maintenance pause %s released and device returned to service", item.ID),
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = contracts.PauseResponse{Pause: item}
		return nil
	})
	if err != nil {
		return contracts.PauseResponse{}, err
	}
	if expired != nil {
		return contracts.PauseResponse{}, expired
	}
	return result, nil
}

func (s *Service) Pause(ctx context.Context, pauseID string) (contracts.PauseResponse, error) {
	var result contracts.PauseResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Pause(strings.TrimSpace(pauseID))
		if !exists {
			return fmt.Errorf("%w: maintenance pause %s", domain.ErrNotFound, pauseID)
		}
		result = contracts.PauseResponse{Pause: projectedPause(item, s.now())}
		return nil
	})
	return result, err
}

func (s *Service) DevicePauses(ctx context.Context, deviceID string) (contracts.PausesResponse, error) {
	var result contracts.PausesResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		id := strings.TrimSpace(deviceID)
		if _, exists := snapshot.Device(id); !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		now := s.now()
		items := snapshot.PausesForDevice(id)
		result.Pauses = make([]domain.MaintenancePause, 0, len(items))
		for _, item := range items {
			result.Pauses = append(result.Pauses, projectedPause(item, now))
		}
		return nil
	})
	return result, err
}

func (s *Service) DeviceEvents(ctx context.Context, deviceID string) (contracts.EventsResponse, error) {
	var result contracts.EventsResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		id := strings.TrimSpace(deviceID)
		if _, exists := snapshot.Device(id); !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		result.Events = snapshot.DeviceEvents(id)
		return nil
	})
	return result, err
}

// settleDevicePause persists an automatic expiry for the device's stored
// active pause when it is due, appending the audit event in the same commit.
// It returns the settled pause when an expiry happened. Every gating decision
// calls this first so the stored fact is always current.
func (s *Service) settleDevicePause(m *repository.Mutation, deviceID string, now time.Time) (domain.MaintenancePause, bool, error) {
	item, exists := m.ActivePauseForDevice(deviceID)
	if !exists || !item.IsDue(now) {
		return domain.MaintenancePause{}, false, nil
	}
	if err := item.Expire(now); err != nil {
		return domain.MaintenancePause{}, false, err
	}
	if err := m.UpdatePause(item); err != nil {
		return domain.MaintenancePause{}, false, err
	}
	event, err := domain.NewAuditEvent(
		m.NextID("event"),
		"device",
		deviceID,
		"",
		"maintenance_pause_expired",
		"system",
		fmt.Sprintf("maintenance pause %s expired automatically at %s",
			item.ID, item.ExpiresAt.Format(time.RFC3339)),
		now,
	)
	if err != nil {
		return domain.MaintenancePause{}, false, err
	}
	m.AppendEvent(event)
	return item, true, nil
}

func (s *Service) recordBlockedOpen(m *repository.Mutation, pause domain.MaintenancePause, steward string, now time.Time) error {
	actor := strings.TrimSpace(steward)
	if actor == "" {
		actor = "unknown"
	}
	event, err := domain.NewAuditEvent(
		m.NextID("event"),
		"device",
		pause.DeviceID,
		"",
		"case_open_blocked",
		actor,
		fmt.Sprintf("new verification case blocked by %s maintenance pause %s until %s",
			pause.Status, pause.ID, pause.ExpiresAt.Format(time.RFC3339)),
		now,
	)
	if err != nil {
		return err
	}
	m.AppendEvent(event)
	return nil
}

func resolvePauseExpiry(input contracts.StartPauseRequest, now time.Time) (time.Time, error) {
	hasDuration := input.DurationMinutes != 0
	hasExpiry := input.ExpiresAt != nil
	if hasDuration == hasExpiry {
		return time.Time{}, fmt.Errorf("%w: provide exactly one of duration_minutes or expires_at", domain.ErrInvalidInput)
	}
	if hasDuration {
		if input.DurationMinutes < 1 {
			return time.Time{}, fmt.Errorf("%w: duration_minutes must be positive", domain.ErrInvalidInput)
		}
		duration := time.Duration(input.DurationMinutes) * time.Minute
		if duration > domain.MaxPauseDuration {
			return time.Time{}, fmt.Errorf("%w: maintenance pause cannot exceed %s", domain.ErrInvalidInput, domain.MaxPauseDuration)
		}
		return now.Add(duration).UTC(), nil
	}
	expiry := input.ExpiresAt.UTC()
	if !expiry.After(now) {
		return time.Time{}, fmt.Errorf("%w: expires_at must be in the future", domain.ErrInvalidInput)
	}
	if expiry.Sub(now) > domain.MaxPauseDuration {
		return time.Time{}, fmt.Errorf("%w: maintenance pause cannot exceed %s", domain.ErrInvalidInput, domain.MaxPauseDuration)
	}
	return expiry, nil
}

// effectivePause returns the pause that actually blocks new work at time now:
// a stored active pause that has not reached its expiry. Callers must settle
// due pauses before relying on this inside a mutation.
func effectivePause(m *repository.Mutation, deviceID string, now time.Time) (domain.MaintenancePause, bool) {
	item, exists := m.ActivePauseForDevice(deviceID)
	if !exists {
		return domain.MaintenancePause{}, false
	}
	if !item.ExpiresAt.After(now) {
		return domain.MaintenancePause{}, false
	}
	return item, true
}

// projectedPause reports an active pause as expired on read without mutating
// stored history; the authoritative expiry is persisted on the next write.
func projectedPause(item domain.MaintenancePause, now time.Time) domain.MaintenancePause {
	if item.IsDue(now) {
		end := item.ExpiresAt
		item.Status = domain.PauseExpired
		item.EndedAt = &end
	}
	return item
}

func bestAcceptedMeasurement(items []domain.Measurement) (domain.Measurement, bool) {
	var selected domain.Measurement
	found := false
	for _, item := range items {
		if !item.Accepted {
			continue
		}
		if !found || item.AbsoluteDeviation() < selected.AbsoluteDeviation() {
			selected = item
			found = true
		}
	}
	return selected, found
}

func caseResponse(m *repository.Mutation, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: m.Measurements(item.ID),
		Events:       m.Events(item.ID),
	}
	if seal, exists := m.Seal(item.ID); exists {
		result.Seal = &seal
	}
	return result
}

func caseResponseFromSnapshot(snapshot repository.Snapshot, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: snapshot.Measurements(item.ID),
		Events:       snapshot.Events(item.ID),
	}
	if seal, exists := snapshot.Seal(item.ID); exists {
		result.Seal = &seal
	}
	return result
}

func deviceResponseFromSnapshot(snapshot repository.Snapshot, item domain.Device, now time.Time) contracts.DeviceResponse {
	result := contracts.DeviceResponse{Device: item}
	if pause, exists := snapshot.ActivePauseForDevice(item.ID); exists {
		projected := projectedPause(pause, now)
		if projected.Status == domain.PauseActive {
			result.CurrentPause = &projected
		}
	}
	return result
}

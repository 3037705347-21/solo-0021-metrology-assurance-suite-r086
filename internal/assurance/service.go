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

func (s *Service) RegisterDevice(ctx context.Context, input contracts.RegisterDeviceRequest) (domain.Device, error) {
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
	return created, err
}

func (s *Service) Device(ctx context.Context, id string) (contracts.DeviceResponse, error) {
	var result contracts.DeviceResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		deviceID := strings.TrimSpace(id)
		device, exists := m.Device(deviceID)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if err := s.materializeDeviceExpiry(m, deviceID); err != nil {
			return err
		}
		deviceView(m, device, &result)
		return nil
	})
	return result, err
}

func (s *Service) OpenCase(ctx context.Context, input contracts.OpenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		deviceID := strings.TrimSpace(input.DeviceID)
		steward := strings.TrimSpace(input.Steward)
		if steward == "" {
			return fmt.Errorf("%w: case steward is required", domain.ErrInvalidInput)
		}
		device, exists := m.Device(deviceID)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, deviceID)
		}
		if err := s.materializeDeviceExpiry(m, device.ID); err != nil {
			return err
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		if active, exists := m.ActiveSuspensionForDevice(device.ID); exists {
			event, err := domain.NewAuditEvent(
				m.NextID("event"),
				"device",
				device.ID,
				"",
				"case_open_blocked",
				steward,
				fmt.Sprintf("case open blocked by maintenance suspension %s (%s)", active.ID, active.Reason),
				s.now(),
			)
			if err != nil {
				return err
			}
			// The rejection is itself a device fact: commit the audit event in
			// this atomic transaction, then translate the committed outcome
			// into an API error after the commit succeeds.
			m.AppendEvent(event)
			result = contracts.CaseResponse{}
			return repository.MarkCommitted(fmt.Errorf("%w: device %s is suspended until %s", domain.ErrDeviceSuspended, device.ID, active.ExpiresAt.Format(time.RFC3339)))
		}
		if active, exists := m.ActiveCaseForDevice(device.ID); exists {
			return fmt.Errorf("%w: case %s is still %s", domain.ErrActiveCaseExists, active.ID, active.State)
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			input.Nominal,
			input.Tolerance,
			steward,
			s.now(),
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
			s.now(),
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
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := item.RecordMeasurement(measurement.Accepted, s.now()); err != nil {
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
			s.now(),
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
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if err := item.Reopen(s.now()); err != nil {
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
			s.now(),
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
		if err := item.Seal(s.now()); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(
			m.NextID("seal"),
			item.ID,
			item.DeviceID,
			measurement.ID,
			input.Reviewer,
			input.Summary,
			s.now(),
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
			s.now(),
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

// materializeDeviceExpiry turns a time limit into a stored fact inside the
// current commit. It runs under the repository mutation lock, so an automatic
// expiry and a concurrent open-case or release request always resolve to a
// single outcome. A no-op commit is harmless when nothing has expired.
func (s *Service) materializeDeviceExpiry(m *repository.Mutation, deviceID string) error {
	active, exists := m.ActiveSuspensionForDevice(deviceID)
	if !exists || active.IsActiveAt(s.now()) {
		return nil
	}
	if err := active.Expire(s.now()); err != nil {
		return err
	}
	if err := m.UpdateSuspension(active); err != nil {
		return err
	}
	event, err := domain.NewAuditEvent(
		m.NextID("event"),
		"device",
		deviceID,
		"",
		"suspension_expired",
		active.Actor,
		fmt.Sprintf("maintenance suspension %s reached its expiry time", active.ID),
		s.now(),
	)
	if err != nil {
		return err
	}
	m.AppendEvent(event)
	return nil
}

func (s *Service) StartSuspension(ctx context.Context, deviceID string, input contracts.StartSuspensionRequest) (contracts.SuspensionResponse, error) {
	var result contracts.SuspensionResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(deviceID)
		device, exists := m.Device(id)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if err := s.materializeDeviceExpiry(m, device.ID); err != nil {
			return err
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		if active, exists := m.ActiveSuspensionForDevice(device.ID); exists {
			return fmt.Errorf("%w: suspension %s is already active for device %s", domain.ErrStateConflict, active.ID, device.ID)
		}
		suspension, err := domain.NewMaintenanceSuspension(
			m.NextID("susp"),
			device.ID,
			input.Reason,
			input.Actor,
			s.now(),
			input.ExpiresAt,
		)
		if err != nil {
			return err
		}
		if err := m.PutSuspension(suspension); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			device.ID,
			"",
			"suspension_started",
			suspension.Actor,
			fmt.Sprintf("maintenance suspension %s started until %s: %s", suspension.ID, suspension.ExpiresAt.Format(time.RFC3339), suspension.Reason),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result.Suspension = suspension
		return nil
	})
	return result, err
}

func (s *Service) ReleaseSuspension(ctx context.Context, deviceID string, input contracts.ReleaseSuspensionRequest) (contracts.SuspensionResponse, error) {
	var result contracts.SuspensionResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(deviceID)
		device, exists := m.Device(id)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if err := s.materializeDeviceExpiry(m, device.ID); err != nil {
			return err
		}
		active, exists := m.ActiveSuspensionForDevice(device.ID)
		if !exists {
			return fmt.Errorf("%w: device %s has no active maintenance suspension", domain.ErrStateConflict, device.ID)
		}
		actor := strings.TrimSpace(input.Actor)
		if actor == "" {
			return fmt.Errorf("%w: actor is required", domain.ErrInvalidInput)
		}
		note := strings.TrimSpace(input.Note)
		if note == "" {
			note = "maintenance completed and device returned to service"
		}
		if err := active.Release(s.now(), note); err != nil {
			return err
		}
		if err := m.UpdateSuspension(active); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			device.ID,
			"",
			"suspension_released",
			actor,
			fmt.Sprintf("maintenance suspension %s released: %s", active.ID, active.EndedReason),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result.Suspension = active
		return nil
	})
	return result, err
}

func (s *Service) Suspensions(ctx context.Context, deviceID string) (contracts.SuspensionsResponse, error) {
	var result contracts.SuspensionsResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(deviceID)
		if _, exists := m.Device(id); !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if err := s.materializeDeviceExpiry(m, id); err != nil {
			return err
		}
		result.Suspensions = m.SuspensionsForDevice(id)
		return nil
	})
	return result, err
}

func (s *Service) DeviceEvents(ctx context.Context, deviceID string) (contracts.DeviceEventsResponse, error) {
	var result contracts.DeviceEventsResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(deviceID)
		if _, exists := m.Device(id); !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		if err := s.materializeDeviceExpiry(m, id); err != nil {
			return err
		}
		result.Events = m.DeviceEvents(id)
		return nil
	})
	return result, err
}

func deviceView(m *repository.Mutation, device domain.Device, result *contracts.DeviceResponse) {
	result.Device = device
	result.Suspensions = m.SuspensionsForDevice(device.ID)
	if active, exists := m.ActiveSuspensionForDevice(device.ID); exists {
		result.Suspension = &active
	}
	result.Events = m.DeviceEvents(device.ID)
}

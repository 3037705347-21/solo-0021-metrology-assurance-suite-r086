package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
)

type Repository struct {
	mu           sync.RWMutex
	devices      map[string]domain.Device
	cases        map[string]domain.VerificationCase
	measurements map[string][]domain.Measurement
	seals        map[string]domain.EvidenceSeal
	suspensions  map[string]domain.MaintenanceSuspension
	events       []domain.AuditEvent
	counters     map[string]int
}

func New() *Repository {
	return &Repository{
		devices:      make(map[string]domain.Device),
		cases:        make(map[string]domain.VerificationCase),
		measurements: make(map[string][]domain.Measurement),
		seals:        make(map[string]domain.EvidenceSeal),
		suspensions:  make(map[string]domain.MaintenanceSuspension),
		counters:     make(map[string]int),
	}
}

type Snapshot struct {
	devices      map[string]domain.Device
	cases        map[string]domain.VerificationCase
	measurements map[string][]domain.Measurement
	seals        map[string]domain.EvidenceSeal
	suspensions  map[string]domain.MaintenanceSuspension
	events       []domain.AuditEvent
}

func (s Snapshot) Device(id string) (domain.Device, bool) {
	item, ok := s.devices[id]
	return item, ok
}

func (s Snapshot) DeviceByTag(tag string) (domain.Device, bool) {
	for _, item := range s.devices {
		if item.AssetTag == tag {
			return item, true
		}
	}
	return domain.Device{}, false
}

func (s Snapshot) Case(id string) (domain.VerificationCase, bool) {
	item, ok := s.cases[id]
	return item, ok
}

func (s Snapshot) Measurements(caseID string) []domain.Measurement {
	return append([]domain.Measurement(nil), s.measurements[caseID]...)
}

func (s Snapshot) Seal(caseID string) (domain.EvidenceSeal, bool) {
	item, ok := s.seals[caseID]
	return item, ok
}

func (s Snapshot) Events(caseID string) []domain.AuditEvent {
	var result []domain.AuditEvent
	for _, item := range s.events {
		if item.CaseID == caseID {
			result = append(result, item)
		}
	}
	return result
}

func (s Snapshot) SuspensionsForDevice(deviceID string) []domain.MaintenanceSuspension {
	result := make([]domain.MaintenanceSuspension, 0)
	for _, item := range s.suspensions {
		if item.DeviceID == deviceID {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

func (s Snapshot) ActiveSuspensionForDevice(deviceID string) (domain.MaintenanceSuspension, bool) {
	var found domain.MaintenanceSuspension
	exists := false
	for _, item := range s.suspensions {
		if item.DeviceID != deviceID || item.Status != domain.SuspensionActive {
			continue
		}
		if !exists || item.StartedAt.After(found.StartedAt) {
			found = item
			exists = true
		}
	}
	return found, exists
}

func (s Snapshot) DeviceEvents(deviceID string) []domain.AuditEvent {
	result := make([]domain.AuditEvent, 0)
	for _, item := range s.events {
		if item.SubjectType == "device" && item.SubjectID == deviceID {
			result = append(result, item)
		}
	}
	return result
}

func (s Snapshot) AllDevices() []domain.Device {
	result := make([]domain.Device, 0, len(s.devices))
	for _, item := range s.devices {
		result = append(result, item)
	}
	return result
}

func (s Snapshot) AllCases() []domain.VerificationCase {
	result := make([]domain.VerificationCase, 0, len(s.cases))
	for _, item := range s.cases {
		result = append(result, item)
	}
	return result
}

type Mutation struct {
	devices      map[string]domain.Device
	cases        map[string]domain.VerificationCase
	measurements map[string][]domain.Measurement
	seals        map[string]domain.EvidenceSeal
	suspensions  map[string]domain.MaintenanceSuspension
	events       []domain.AuditEvent
	counters     map[string]int
}

func (m *Mutation) NextID(prefix string) string {
	m.counters[prefix]++
	return fmt.Sprintf("%s-%04d", prefix, m.counters[prefix])
}

func (m *Mutation) Device(id string) (domain.Device, bool) {
	item, ok := m.devices[id]
	return item, ok
}

func (m *Mutation) DeviceByTag(tag string) (domain.Device, bool) {
	for _, item := range m.devices {
		if item.AssetTag == tag {
			return item, true
		}
	}
	return domain.Device{}, false
}

func (m *Mutation) PutDevice(item domain.Device) error {
	if _, exists := m.devices[item.ID]; exists {
		return fmt.Errorf("%w: device %s", domain.ErrStateConflict, item.ID)
	}
	m.devices[item.ID] = item
	return nil
}

func (m *Mutation) UpdateDevice(item domain.Device) error {
	if _, exists := m.devices[item.ID]; !exists {
		return fmt.Errorf("%w: device %s", domain.ErrNotFound, item.ID)
	}
	m.devices[item.ID] = item
	return nil
}

func (m *Mutation) Case(id string) (domain.VerificationCase, bool) {
	item, ok := m.cases[id]
	return item, ok
}

func (m *Mutation) PutCase(item domain.VerificationCase) error {
	if _, exists := m.cases[item.ID]; exists {
		return fmt.Errorf("%w: case %s", domain.ErrStateConflict, item.ID)
	}
	m.cases[item.ID] = item
	return nil
}

func (m *Mutation) UpdateCase(item domain.VerificationCase) error {
	if _, exists := m.cases[item.ID]; !exists {
		return fmt.Errorf("%w: case %s", domain.ErrNotFound, item.ID)
	}
	m.cases[item.ID] = item
	return nil
}

func (m *Mutation) ActiveCaseForDevice(deviceID string) (domain.VerificationCase, bool) {
	for _, item := range m.cases {
		if item.DeviceID == deviceID && item.IsUnfinished() {
			return item, true
		}
	}
	return domain.VerificationCase{}, false
}

func (m *Mutation) AppendMeasurement(item domain.Measurement) error {
	if _, exists := m.cases[item.CaseID]; !exists {
		return fmt.Errorf("%w: case %s", domain.ErrNotFound, item.CaseID)
	}
	m.measurements[item.CaseID] = append(m.measurements[item.CaseID], item)
	return nil
}

func (m *Mutation) Measurements(caseID string) []domain.Measurement {
	return append([]domain.Measurement(nil), m.measurements[caseID]...)
}

func (m *Mutation) Seal(caseID string) (domain.EvidenceSeal, bool) {
	item, ok := m.seals[caseID]
	return item, ok
}

func (m *Mutation) PutSeal(item domain.EvidenceSeal) error {
	if _, exists := m.seals[item.CaseID]; exists {
		return fmt.Errorf("%w: case %s", domain.ErrSealAlreadyCreated, item.CaseID)
	}
	m.seals[item.CaseID] = item
	return nil
}

func (m *Mutation) AppendEvent(item domain.AuditEvent) {
	m.events = append(m.events, item)
}

func (m *Mutation) Events(caseID string) []domain.AuditEvent {
	var result []domain.AuditEvent
	for _, item := range m.events {
		if item.CaseID == caseID {
			result = append(result, item)
		}
	}
	return result
}

func (m *Mutation) Suspension(id string) (domain.MaintenanceSuspension, bool) {
	item, ok := m.suspensions[id]
	return item, ok
}

func (m *Mutation) PutSuspension(item domain.MaintenanceSuspension) error {
	if _, exists := m.suspensions[item.ID]; exists {
		return fmt.Errorf("%w: suspension %s", domain.ErrStateConflict, item.ID)
	}
	m.suspensions[item.ID] = item
	return nil
}

func (m *Mutation) UpdateSuspension(item domain.MaintenanceSuspension) error {
	if _, exists := m.suspensions[item.ID]; !exists {
		return fmt.Errorf("%w: suspension %s", domain.ErrNotFound, item.ID)
	}
	m.suspensions[item.ID] = item
	return nil
}

func (m *Mutation) SuspensionsForDevice(deviceID string) []domain.MaintenanceSuspension {
	result := make([]domain.MaintenanceSuspension, 0)
	for _, item := range m.suspensions {
		if item.DeviceID == deviceID {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

func (m *Mutation) ActiveSuspensionForDevice(deviceID string) (domain.MaintenanceSuspension, bool) {
	var found domain.MaintenanceSuspension
	exists := false
	for _, item := range m.suspensions {
		if item.DeviceID != deviceID || item.Status != domain.SuspensionActive {
			continue
		}
		if !exists || item.StartedAt.After(found.StartedAt) {
			found = item
			exists = true
		}
	}
	return found, exists
}

func (m *Mutation) DeviceEvents(deviceID string) []domain.AuditEvent {
	result := make([]domain.AuditEvent, 0)
	for _, item := range m.events {
		if item.SubjectType == "device" && item.SubjectID == deviceID {
			result = append(result, item)
		}
	}
	return result
}

func (r *Repository) Read(ctx context.Context, fn func(Snapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fn(r.snapshot())
}

// ErrCommitted marks an operation outcome whose mutations must be committed
// even though the callback returns an error. Use it when a rejection is itself
// a recorded fact (for example a blocked request audited inside the same
// commit). Wrap the caller-facing error with ErrCommitted; the commit proceeds
// and the wrapped error is returned to the caller.
var ErrCommitted = errors.New("outcome committed")

type CommittedError struct {
	cause error
}

func (e *CommittedError) Error() string {
	return e.cause.Error()
}

func (e *CommittedError) Unwrap() error {
	return e.cause
}

// Is recognizes both the internal commit marker and any error wrapped by the
// caller-facing cause.
func (e *CommittedError) Is(target error) bool {
	return target == ErrCommitted || errors.Is(e.cause, target)
}

// MarkCommitted wraps err so that a Mutate callback can persist its changes
// while still surfacing err as the operation result.
func MarkCommitted(err error) error {
	if err == nil {
		return nil
	}
	return &CommittedError{cause: err}
}

func (r *Repository) Mutate(ctx context.Context, fn func(*Mutation) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	next := &Mutation{
		devices:      cloneDevices(r.devices),
		cases:        cloneCases(r.cases),
		measurements: cloneMeasurements(r.measurements),
		seals:        cloneSeals(r.seals),
		suspensions:  cloneSuspensions(r.suspensions),
		events:       append([]domain.AuditEvent(nil), r.events...),
		counters:     cloneCounters(r.counters),
	}
	callbackErr := fn(next)
	if callbackErr != nil && !errors.Is(callbackErr, ErrCommitted) {
		return callbackErr
	}
	r.devices = next.devices
	r.cases = next.cases
	r.measurements = next.measurements
	r.seals = next.seals
	r.suspensions = next.suspensions
	r.events = next.events
	r.counters = next.counters
	if callbackErr != nil {
		// The commit succeeded; surface only the caller-facing cause without
		// the internal commit marker.
		return errors.Unwrap(callbackErr)
	}
	return nil
}

func (r *Repository) snapshot() Snapshot {
	return Snapshot{
		devices:      cloneDevices(r.devices),
		cases:        cloneCases(r.cases),
		measurements: cloneMeasurements(r.measurements),
		seals:        cloneSeals(r.seals),
		suspensions:  cloneSuspensions(r.suspensions),
		events:       append([]domain.AuditEvent(nil), r.events...),
	}
}

func cloneDevices(values map[string]domain.Device) map[string]domain.Device {
	result := make(map[string]domain.Device, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneCases(values map[string]domain.VerificationCase) map[string]domain.VerificationCase {
	result := make(map[string]domain.VerificationCase, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneMeasurements(values map[string][]domain.Measurement) map[string][]domain.Measurement {
	result := make(map[string][]domain.Measurement, len(values))
	for key, value := range values {
		result[key] = append([]domain.Measurement(nil), value...)
	}
	return result
}

func cloneSeals(values map[string]domain.EvidenceSeal) map[string]domain.EvidenceSeal {
	result := make(map[string]domain.EvidenceSeal, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneSuspensions(values map[string]domain.MaintenanceSuspension) map[string]domain.MaintenanceSuspension {
	result := make(map[string]domain.MaintenanceSuspension, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneCounters(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

package contracts

import (
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
)

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

type OpenCaseRequest struct {
	DeviceID  string  `json:"device_id"`
	Nominal   float64 `json:"nominal"`
	Tolerance float64 `json:"tolerance"`
	Steward   string  `json:"steward"`
}

type RecordMeasurementRequest struct {
	Observed   float64 `json:"observed"`
	Technician string  `json:"technician"`
}

type ReopenCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
}

// StartPauseRequest bounds a maintenance window either by duration or by an
// explicit RFC3339 expiry. Exactly one of the two must be present.
type StartPauseRequest struct {
	Reason          string     `json:"reason"`
	RequestedBy     string     `json:"requested_by"`
	DurationMinutes int        `json:"duration_minutes,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
}

type ReleasePauseRequest struct {
	ReleasedBy string `json:"released_by"`
}

type DeviceResponse struct {
	Device       domain.Device            `json:"device"`
	CurrentPause *domain.MaintenancePause `json:"current_pause,omitempty"`
}

type CaseResponse struct {
	Case         domain.VerificationCase `json:"case"`
	Measurements []domain.Measurement    `json:"measurements"`
	Seal         *domain.EvidenceSeal    `json:"seal,omitempty"`
	Events       []domain.AuditEvent     `json:"events"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type PauseResponse struct {
	Pause domain.MaintenancePause `json:"pause"`
}

type PausesResponse struct {
	Pauses []domain.MaintenancePause `json:"pauses"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

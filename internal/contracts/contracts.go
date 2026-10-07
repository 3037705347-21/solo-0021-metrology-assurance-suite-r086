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

type StartSuspensionRequest struct {
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ReleaseSuspensionRequest struct {
	Actor string `json:"actor"`
	Note  string `json:"note"`
}

type DeviceResponse struct {
	Device      domain.Device                  `json:"device"`
	Suspensions []domain.MaintenanceSuspension `json:"suspensions"`
	Suspension  *domain.MaintenanceSuspension  `json:"suspension,omitempty"`
	Events      []domain.AuditEvent            `json:"events"`
}

type SuspensionResponse struct {
	Suspension domain.MaintenanceSuspension `json:"suspension"`
}

type SuspensionsResponse struct {
	Suspensions []domain.MaintenanceSuspension `json:"suspensions"`
}

type DeviceEventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
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

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

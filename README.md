# Metrology Assurance Suite

Metrology Assurance Suite is a Go HTTP service for bounded laboratory
measurement assurance work. It registers devices, opens verification cases,
records measurements, handles failed tolerance decisions, and seals traceable
evidence records.

## Run

```bash
go run ./cmd/metrology-service --addr 127.0.0.1:18121
```

The service listens on `127.0.0.1:18121` by default.

## Build And Verify

```bash
go build ./...
go run ./cmd/workflowcheck --workflow register-device
go run ./cmd/workflowcheck --workflow open-verification-case
go run ./cmd/workflowcheck --workflow record-measurement
go run ./cmd/workflowcheck --workflow reopen-verification-case
go run ./cmd/workflowcheck --workflow seal-verification
go run ./cmd/workflowcheck --workflow maintenance-pause
```

The workflow runner starts a fresh in-process HTTP service and exercises the
public API. It is a production smoke command, not a unit test. The
maintenance-pause check drives a controllable clock so automatic expiry and
concurrent decisions are verified deterministically.

## API

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `GET /devices/{id}/events`
- `POST /devices/{id}/maintenance-pauses`
- `GET /devices/{id}/maintenance-pauses`
- `GET /maintenance-pauses/{pauseId}`
- `POST /maintenance-pauses/{pauseId}/release`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Maintenance Pause

A steward can put an active device on a bounded maintenance pause instead of
leaving requests ambiguous:

```json
POST /devices/device-0001/maintenance-pauses
{
  "reason": "scheduled calibration bench maintenance",
  "requested_by": "Lin",
  "duration_minutes": 30
}
```

Either `duration_minutes` or an RFC3339 `expires_at` bounds the window. While
the pause is effective, `POST /cases` for the device fails with
`maintenance_pause_active` and each blocked attempt is appended to the device
audit trail as `case_open_blocked`. Repeating a pause while one is effective
also conflicts.

A pause ends in exactly one way: `POST /maintenance-pauses/{pauseId}/release`
before expiry yields `released`; after the expiry instant it settles
automatically as `expired` (a late release cannot override it). Release or
expiry re-admits new cases against the device's current facts. Unfinished
cases, seals, full pause history (`GET /devices/{id}/maintenance-pauses`), and
audit events remain readable throughout.

## Tests

```bash
go test ./...
```

Unit tests cover the maintenance pause lifecycle in `internal/domain` and the
service decisions (blocked opens with audit, release vs. automatic expiry,
history retention, window validation, and concurrent outcomes) in
`internal/assurance`.

## Layout

- `cmd/metrology-service`: executable HTTP server.
- `cmd/workflowcheck`: bounded workflow verification commands.
- `internal/httpapi`: route and JSON boundary.
- `internal/assurance`: workflow orchestration.
- `internal/domain`: state rules and invariants.
- `internal/repository`: copy-on-write in-memory persistence.
- `internal/contracts`: API request and response models.
- `internal/bootstrap`: concrete dependency wiring.

## Inputs And Outputs

Inputs are JSON HTTP requests. Outputs are JSON response envelopes with either a
resource view or a stable error object:

```json
{
  "error": {
    "code": "state_conflict",
    "message": "case is not awaiting measurement"
  }
}
```

The service has no environment variables and no external dependencies.

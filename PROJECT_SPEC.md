# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices, opens a
verification case for one device, records technician measurements, requests a
repeat measurement when tolerance is exceeded, and seals the final evidence
record after review.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices and opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, version, and assigned steward.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement.
- MaintenanceSuspension: a time-bounded maintenance pause attached to a device,
  with a reason, actor, start time, expiry limit, and an active, released, or
  expired status. It is a fact beside the device lifecycle rather than a device
  state: an active device remains `active` while suspended.
- AuditEvent: an append-only trace of meaningful case and device changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, tolerance, and
owner. The service verifies that the device exists, is active, and has no
unfinished case. A valid request creates a case in `awaiting_measurement`.

### 3. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement`. The service computes the signed deviation from the
nominal value. An in-tolerance measurement moves the case to
`review_ready`; an out-of-tolerance measurement moves it to
`recheck_required`.

### 4. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 5. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute deviation, creates exactly one evidence
seal, moves the case to `sealed`, and rejects later mutations.

### 6. Suspend Device For Maintenance

The steward sends `POST /devices/{id}/suspensions` with a reason, actor, and
expiry time. The device must exist and be active, and it must not already have
an active suspension. A valid request creates an `active` suspension and
appends an audit event. While a suspension is active, `POST /cases` for the
device is rejected with `device_suspended`; every blocked open appends a
`case_open_blocked` audit event in the same commit as the rejection. Existing
unfinished cases, historical suspensions, and audit records stay readable.

The steward ends the pause with `POST /devices/{id}/suspensions/release`. A
suspension whose expiry time has passed is materialized as `expired`
automatically inside the next locked repository operation. After release or
expiry, opening a case is admitted again by re-evaluating the current device
facts (active device and no unfinished case); an end-of-pause never clears an
unfinished case. Repeated suspensions, automatic expiry, and a pause racing a
case open all resolve to exactly one outcome under the repository mutation
lock.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.
- A maintenance suspension is recorded independently of device state: an
  `active` suspension blocks new cases while the device itself stays `active`.

MaintenanceSuspension state:

- `active -> released` by an explicit release.
- `active -> expired` when the current time reaches `expires_at`, materialized
  atomically on the next repository operation that reads the device.
- `released` and `expired` are terminal; a new pause is a new suspension record.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- A device has at most one unfinished verification case.
- Measurements are accepted only while a case is awaiting measurement.
- A tolerance must be positive.
- A case can have at most one evidence seal.
- A sealed case cannot accept measurements, reopen, or seal again.
- A device has at most one active maintenance suspension.
- A suspension requires a reason, actor, and an expiry time after its start.
- An active suspension rejects new cases for the device and records each
  rejection as an auditable device fact in the same repository commit.
- Release or expiry never changes device lifecycle state and never clears
  unfinished cases; re-admission is decided from the current device facts.
- Expired, released, and historical suspensions stay readable.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /devices/{id}/suspensions`
- `POST /devices/{id}/suspensions/release`
- `GET /devices/{id}/suspensions`
- `GET /devices/{id}/events`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The maintenance suspension check pauses a device, audits a blocked open,
releases the pause and re-admits against current facts, verifies automatic
expiry and re-suspension, and races duplicate pauses together with concurrent
blocked opens to prove a single deterministic outcome.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.

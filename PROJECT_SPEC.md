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
- MaintenancePause: a bounded downtime fact for one device with a reason, the
  requesting steward, start/expiry instants, and an `active`, `released`, or
  `expired` status. Terminal records are retained as history.
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

### 6. Maintenance Pause

The steward sends `POST /devices/{id}/maintenance-pauses` with a reason, the
requesting steward, and a time bound (either `duration_minutes` or an explicit
`expires_at`, but not both). Only an active device without an effective pause
can be paused. The pause is a formal device fact with a fixed start and
expiry.

While a pause is effective, no new verification case can be opened for the
device. Each blocked attempt appends a `case_open_blocked` audit event, so the
period can later be reconciled against the cases that were refused. Repeated
pause requests conflict while one is effective.

A pause ends in exactly one of two ways:

- The steward sends `POST /maintenance-pauses/{pauseId}/release` before the
  expiry instant and the pause becomes `released`.
- The expiry instant passes and the next device-touching decision settles it
  atomically as `expired`, appending a `maintenance_pause_expired` event. A
  release that arrives at or after expiry cannot override this outcome.

After release or expiry, new cases are admitted against the device's facts at
that moment (active device, no effective pause, no unfinished case). Existing
unfinished cases, sealed history, the full pause history, and the audit trail
remain readable. Pauses never delete evidence or cases.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

MaintenancePause state:

- `active -> released` (steward release strictly before expiry)
- `active -> expired` (automatic, at the scheduled expiry instant)
- `released` and `expired` are terminal; the record is retained as history.

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
- Every mutation that changes a case also appends an audit event in the same
  repository commit.
- A device has at most one effective maintenance pause; `active` is the only
  blocking status and it stops blocking at the expiry instant.
- A pause requires a non-empty reason, requesting steward, start, and a later
  expiry bounded to short-term downtime.
- A pause reaches exactly one terminal state: a release strictly before expiry
  or an automatic expiry at the scheduled instant.
- Expiry settlement, admission, and the blocked-open audit event are decided
  inside one repository commit under the mutation lock, so concurrent pause
  and open requests serialize to a single result.
- Releasing, expiring, or blocking work never deletes cases, measurements,
  seals, prior pauses, or audit events.

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

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check races two concurrent seal
requests and the maintenance-pause check races pause creation against case
opening and duplicate pause requests against each other to prove that the
repository mutation lock permits exactly one outcome. The pause checks drive a
controllable in-process clock so automatic expiry is deterministic.

The maintenance extension ships with unit tests under `internal/domain` and
`internal/assurance`, covering pause validation, the release/expiry boundary,
audited blocked opens, history retention, and the concurrent decision. Tests
for the original baseline workflows remain deferred to the engineering-task
stage, alongside extending the suite to cover their remaining failure paths.

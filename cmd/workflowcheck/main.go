package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
	clock   *mutableClock
}

// mutableClock lets a single in-process service advance time deterministically
// so pause expiry can be exercised without real sleeps.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutableClock() *mutableClock {
	return &mutableClock{now: time.Now().UTC()}
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":          checkRegisterDevice,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
		"maintenance-pause":        checkMaintenancePause,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func checkMaintenancePause() error {
	if err := checkPauseBlocksAndReleases(); err != nil {
		return fmt.Errorf("block/release: %w", err)
	}
	if err := checkPauseAutoExpiry(); err != nil {
		return fmt.Errorf("auto-expiry: %w", err)
	}
	if err := checkPauseHistoryAndReadability(); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	if err := checkPauseConcurrentOpen(); err != nil {
		return fmt.Errorf("concurrent-open: %w", err)
	}
	if err := checkPauseDuplicateConcurrent(); err != nil {
		return fmt.Errorf("duplicate-concurrent: %w", err)
	}
	return nil
}

func checkPauseBlocksAndReleases() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1501")
	if err != nil {
		return err
	}
	started, err := h.startPause(deviceID, map[string]any{
		"reason":           "scheduled calibration bench maintenance",
		"requested_by":     "Lin",
		"duration_minutes": 30,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(started, http.StatusCreated); err != nil {
		return err
	}
	pauseID, err := nestedString(started.Body, "pause", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(started.Body, "pause", "status")
	if err != nil {
		return err
	}
	if state != "active" {
		return fmt.Errorf("new pause status = %q", state)
	}

	// Device view reports the formal pause fact.
	view, err := h.request(http.MethodGet, "/devices/"+deviceID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(view, http.StatusOK); err != nil {
		return err
	}
	currentID, err := nestedString(view.Body, "current_pause", "id")
	if err != nil {
		return err
	}
	if currentID != pauseID {
		return fmt.Errorf("device current_pause = %q, expected %q", currentID, pauseID)
	}

	// A new case is blocked while the device is paused.
	blocked, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	if err := expectStatus(blocked, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blocked, "maintenance_pause_active"); err != nil {
		return err
	}

	// A second pause while one is effective is rejected.
	duplicate, err := h.startPause(deviceID, map[string]any{
		"reason":           "inspection visit",
		"requested_by":     "Mira",
		"duration_minutes": 45,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(duplicate, "maintenance_pause_active"); err != nil {
		return err
	}

	// Invalid windows are rejected before any fact is created.
	if err := expectBadRequest(h.startPause(deviceID, map[string]any{
		"reason":       "no time bound given",
		"requested_by": "Lin",
	})); err != nil {
		return fmt.Errorf("missing window: %w", err)
	}
	if err := expectBadRequest(h.startPause(deviceID, map[string]any{
		"reason":           "both bounds given",
		"requested_by":     "Lin",
		"duration_minutes": 30,
		"expires_at":       h.clock.Now().Add(time.Hour).Format(time.RFC3339),
	})); err != nil {
		return fmt.Errorf("double window: %w", err)
	}

	// Releasing the pause admits new work against the current device facts.
	released, err := h.request(http.MethodPost, "/maintenance-pauses/"+pauseID+"/release", map[string]any{
		"released_by": "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(released, http.StatusOK); err != nil {
		return err
	}
	releasedState, err := nestedString(released.Body, "pause", "status")
	if err != nil {
		return err
	}
	if releasedState != "released" {
		return fmt.Errorf("released pause status = %q", releasedState)
	}

	readView, err := h.request(http.MethodGet, "/devices/"+deviceID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(readView, http.StatusOK); err != nil {
		return err
	}
	if _, present := readView.Body["current_pause"]; present {
		return errors.New("device still reports a current pause after release")
	}

	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	if err := expectStatus(opened, http.StatusCreated); err != nil {
		return err
	}

	// Releasing again is a conflict, not a second terminal result.
	again, err := h.request(http.MethodPost, "/maintenance-pauses/"+pauseID+"/release", map[string]any{
		"released_by": "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(again, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(again, "maintenance_pause_not_active"); err != nil {
		return err
	}

	// The blocked attempt left an auditable fact on the device timeline.
	events, err := h.request(http.MethodGet, "/devices/"+deviceID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "maintenance_pause_started") ||
		!hasEvent(events.Body, "case_open_blocked") ||
		!hasEvent(events.Body, "maintenance_pause_released") {
		return errors.New("device audit trail is missing pause lifecycle or blocked-open events")
	}
	return nil
}

func checkPauseAutoExpiry() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1502")
	if err != nil {
		return err
	}
	started, err := h.startPause(deviceID, map[string]any{
		"reason":           "short field inspection",
		"requested_by":     "Lin",
		"duration_minutes": 10,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(started, http.StatusCreated); err != nil {
		return err
	}
	pauseID, err := nestedString(started.Body, "pause", "id")
	if err != nil {
		return err
	}

	// Before expiry the case is still blocked.
	blocked, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	if err := expectStatus(blocked, http.StatusConflict); err != nil {
		return err
	}

	// A late release at/after expiry cannot override automatic expiry.
	h.clock.Advance(11 * time.Minute)
	lateRelease, err := h.request(http.MethodPost, "/maintenance-pauses/"+pauseID+"/release", map[string]any{
		"released_by": "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(lateRelease, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(lateRelease, "maintenance_pause_not_active"); err != nil {
		return err
	}

	// The next admission decision settles expiry automatically and admits.
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	if err := expectStatus(opened, http.StatusCreated); err != nil {
		return err
	}

	pauseView, err := h.request(http.MethodGet, "/maintenance-pauses/"+pauseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(pauseView, http.StatusOK); err != nil {
		return err
	}
	status, err := nestedString(pauseView.Body, "pause", "status")
	if err != nil {
		return err
	}
	if status != "expired" {
		return fmt.Errorf("expired pause status = %q", status)
	}

	events, err := h.request(http.MethodGet, "/devices/"+deviceID+"/events", nil)
	if err != nil {
		return err
	}
	if !hasEvent(events.Body, "maintenance_pause_expired") {
		return errors.New("automatic expiry audit event is missing")
	}
	// Only one expiry fact must exist even though expiry was touched twice.
	if countEvent(events.Body, "maintenance_pause_expired") != 1 {
		return errors.New("automatic expiry was recorded more than once")
	}
	return nil
}

func checkPauseHistoryAndReadability() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1503")
	if err != nil {
		return err
	}
	first, err := h.startPause(deviceID, map[string]any{
		"reason":           "first stop",
		"requested_by":     "Lin",
		"duration_minutes": 10,
	})
	if err != nil {
		return err
	}
	firstID, err := nestedString(first.Body, "pause", "id")
	if err != nil {
		return err
	}
	if _, err := h.request(http.MethodPost, "/maintenance-pauses/"+firstID+"/release", map[string]any{
		"released_by": "Lin",
	}); err != nil {
		return err
	}
	second, err := h.startPause(deviceID, map[string]any{
		"reason":           "second stop",
		"requested_by":     "Mira",
		"duration_minutes": 10,
	})
	if err != nil {
		return err
	}
	secondID, err := nestedString(second.Body, "pause", "id")
	if err != nil {
		return err
	}
	h.clock.Advance(11 * time.Minute)
	// Touch the device to settle the second pause's expiry.
	if _, err := h.openCase(deviceID); err != nil {
		return err
	}

	list, err := h.request(http.MethodGet, "/devices/"+deviceID+"/maintenance-pauses", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(list, http.StatusOK); err != nil {
		return err
	}
	pauses, err := nestedSlice(list.Body, "pauses")
	if err != nil {
		return err
	}
	if len(pauses) != 2 {
		return fmt.Errorf("pause history has %d entries, expected 2", len(pauses))
	}
	statuses := []string{}
	for _, item := range pauses {
		object, ok := item.(map[string]any)
		if !ok {
			return errors.New("pause entry is not an object")
		}
		text, _ := object["status"].(string)
		statuses = append(statuses, text)
	}
	if statuses[0] != "released" || statuses[1] != "expired" {
		return fmt.Errorf("pause history statuses = %v, expected [released expired]", statuses)
	}
	if firstID == secondID {
		return errors.New("two pauses share an id")
	}
	return nil
}

func checkPauseConcurrentOpen() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1504")
	if err != nil {
		return err
	}

	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		response, callErr := h.startPause(deviceID, map[string]any{
			"reason":           "race against case open",
			"requested_by":     "Lin",
			"duration_minutes": 30,
		})
		results <- outcome{response: response, err: callErr}
	}()
	wait.Add(1)
	go func() {
		defer wait.Done()
		response, callErr := h.openCase(deviceID)
		results <- outcome{response: response, err: callErr}
	}()
	wait.Wait()
	close(results)

	pauseCreated := 0
	caseCreated := 0
	blockedByPause := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			if _, ok := item.response.Body["pause"]; ok {
				pauseCreated++
			}
			if _, ok := item.response.Body["case"]; ok {
				caseCreated++
			}
		case http.StatusConflict:
			if code, _ := nestedString(item.response.Body, "error", "code"); code == "maintenance_pause_active" {
				blockedByPause++
			}
		default:
			return fmt.Errorf("unexpected concurrent status %d: %s", item.response.Status, item.response.Raw)
		}
	}

	// The single mutation lock serializes the two facts to exactly one order.
	if pauseCreated != 1 {
		return fmt.Errorf("expected exactly one created pause, got %d", pauseCreated)
	}
	switch {
	case caseCreated == 1 && blockedByPause == 0:
		// Open won the race; the pause that followed is a legal later fact.
	case caseCreated == 0 && blockedByPause == 1:
		// Pause won the race; the open was durably rejected and audited.
	default:
		return fmt.Errorf("ambiguous race result: %d cases, %d blocked opens", caseCreated, blockedByPause)
	}
	return nil
}

func checkPauseDuplicateConcurrent() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1505")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, callErr := h.startPause(deviceID, map[string]any{
				"reason":           fmt.Sprintf("duplicate pause request %d", index+1),
				"requested_by":     "Lin",
				"duration_minutes": 30,
			})
			results <- outcome{response: response, err: callErr}
		}(index)
	}
	wait.Wait()
	close(results)

	created := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			if err := expectErrorCode(item.response, "maintenance_pause_active"); err != nil {
				return err
			}
			conflicts++
		default:
			return fmt.Errorf("unexpected duplicate status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if created != 1 || conflicts != 1 {
		return fmt.Errorf("duplicate pause outcomes: %d created, %d conflicts", created, conflicts)
	}

	list, err := h.request(http.MethodGet, "/devices/"+deviceID+"/maintenance-pauses", nil)
	if err != nil {
		return err
	}
	pauses, err := nestedSlice(list.Body, "pauses")
	if err != nil {
		return err
	}
	if len(pauses) != 1 {
		return fmt.Errorf("device has %d stored pauses after duplicate race, expected 1", len(pauses))
	}
	return nil
}

func newHarness() *harness {
	clock := newMutableClock()
	server := httptest.NewServer(bootstrap.NewHandlerWithClock(clock.Now))
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
		clock:   clock,
	}
}
func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) startPause(deviceID string, body map[string]any) (apiResponse, error) {
	return h.request(http.MethodPost, "/devices/"+deviceID+"/maintenance-pauses", body)
}

func expectBadRequest(response apiResponse, err error) error {
	if err != nil {
		return err
	}
	if response.Status != http.StatusBadRequest {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, http.StatusBadRequest, response.Raw)
	}
	return expectErrorCode(response, "invalid_input")
}

func countEvent(root map[string]any, kind string) int {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return 0
	}
	count := 0
	for _, item := range items {
		event, ok := item.(map[string]any)
		if ok && event["kind"] == kind {
			count++
		}
	}
	return count
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}

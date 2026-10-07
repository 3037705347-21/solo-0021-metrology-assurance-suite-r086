package httpapi

import (
	"net/http"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
)

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) registerDevice(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.RegisterDeviceRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.RegisterDevice(request.Context(), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, view)
}

func (s *Server) device(writer http.ResponseWriter, request *http.Request) {
	view, err := s.service.Device(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) openCase(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.OpenCaseRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.OpenCase(request.Context(), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, view)
}

func (s *Server) caseView(writer http.ResponseWriter, request *http.Request) {
	view, err := s.service.Case(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) recordMeasurement(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.RecordMeasurementRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.RecordMeasurement(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, view)
}

func (s *Server) reopenCase(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.ReopenCaseRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.ReopenCase(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) sealCase(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.SealCaseRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.SealCase(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, view)
}

func (s *Server) events(writer http.ResponseWriter, request *http.Request) {
	view, err := s.service.Case(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, contracts.EventsResponse{Events: view.Events})
}

func (s *Server) startPause(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.StartPauseRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.StartMaintenancePause(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, view)
}

func (s *Server) releasePause(writer http.ResponseWriter, request *http.Request) {
	input, err := decodeJSON[contracts.ReleasePauseRequest](writer, request)
	if err != nil {
		writeError(writer, err)
		return
	}
	view, err := s.service.ReleaseMaintenancePause(request.Context(), request.PathValue("pauseId"), input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) pauseView(writer http.ResponseWriter, request *http.Request) {
	view, err := s.service.Pause(request.Context(), request.PathValue("pauseId"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) devicePauses(writer http.ResponseWriter, request *http.Request) {
	view, err := s.service.DevicePauses(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (s *Server) deviceEvents(writer http.ResponseWriter, request *http.Request) {
	view, err := s.service.DeviceEvents(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

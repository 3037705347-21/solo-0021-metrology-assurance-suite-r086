package bootstrap

import (
	"net/http"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/assurance"
	"example.com/solo-0021-metrology-assurance-suite/internal/httpapi"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

func NewHandler() http.Handler {
	return NewHandlerWithClock(time.Now)
}

// NewHandlerWithClock wires the service with a controllable clock. It is used
// by bounded workflow verification to drive maintenance pause expiry.
func NewHandlerWithClock(now func() time.Time) http.Handler {
	repo := repository.New()
	service := assurance.NewServiceWithClock(repo, now)
	return httpapi.NewHandler(service)
}

package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/Mahaveer86619/LocalOps/internal/watchers"
)

type registerWatcherRequest struct {
	Name              string         `json:"name"`
	Kind              string         `json:"kind"`
	ExpectedIntervalS int            `json:"expected_interval_s"`
	// FailThreshold overrides the server-wide consecutive-fails-before-
	// alert default for this watcher; 0 keeps the default. Set this to 1
	// for a continuous checker that only calls back on state change (it
	// has already debounced locally by the time it reports "down").
	FailThreshold int            `json:"fail_threshold,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

func (s *Server) handleRegisterWatcher(c echo.Context) error {
	var req registerWatcherRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errBody(err))
	}
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, errBody(errors.New("name is required")))
	}
	w, err := s.watchers.Store.Register(c.Request().Context(), req.Name, watchers.Kind(req.Kind), req.ExpectedIntervalS, req.FailThreshold, req.Details)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusCreated, w)
}

func (s *Server) handleListWatchers(c echo.Context) error {
	list, err := s.watchers.Store.List(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) handleGetWatcher(c echo.Context) error {
	w, err := s.watchers.Store.Get(c.Request().Context(), c.Param("name"))
	if errors.Is(err, watchers.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, w)
}

type checkinRequest struct {
	State   string         `json:"state"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	// FailThreshold optionally sets/updates this watcher's own alert
	// threshold as a side effect of the check-in (see
	// registerWatcherRequest.FailThreshold) - convenient for a checker
	// that never calls POST /watchers separately.
	FailThreshold *int `json:"fail_threshold,omitempty"`
}

func (s *Server) handleWatcherCheckin(c echo.Context) error {
	var req checkinRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errBody(err))
	}
	state := watchers.State(req.State)
	switch state {
	case watchers.StateOK, watchers.StateDegraded, watchers.StateDown:
	default:
		return c.JSON(http.StatusBadRequest, errBody(errors.New("state must be one of ok, degraded, down")))
	}

	w, err := s.watchers.CheckIn(c.Request().Context(), c.Param("name"), state, req.Message, req.Details, req.FailThreshold)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, w)
}

func (s *Server) handleWatcherEvents(c echo.Context) error {
	events, err := s.watchers.Store.Events(c.Request().Context(), c.Param("name"))
	if errors.Is(err, watchers.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, events)
}

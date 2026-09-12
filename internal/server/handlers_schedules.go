package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
)

// Schedules are a small extension beyond README §14's core API surface,
// backing README §17/Phase 8 (recurring operations) - kept intentionally
// minimal per the same section's explicit "no workflow engine" rule.

type createScheduleRequest struct {
	Name        string `json:"name"`
	Server      string `json:"server"`
	Type        string `json:"type"`
	Description string `json:"description"`
	IntervalS   int    `json:"interval_s"`
}

func (s *Server) handleCreateSchedule(c echo.Context) error {
	var req createScheduleRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errBody(err))
	}
	if req.Name == "" || req.Server == "" || req.Type == "" || req.IntervalS <= 0 {
		return c.JSON(http.StatusBadRequest, errBody(errors.New("name, server, type, and a positive interval_s are required")))
	}
	sched, err := s.schedules.Create(c.Request().Context(), req.Name, req.Server, req.Type, req.Description, req.IntervalS)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusCreated, sched)
}

func (s *Server) handleListSchedules(c echo.Context) error {
	list, err := s.schedules.List(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, list)
}

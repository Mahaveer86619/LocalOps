package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/Mahaveer86619/LocalOps/internal/tasks"
)

type createTaskRequest struct {
	Server             string         `json:"server"`
	Type               string         `json:"type"`
	Description        string         `json:"description"`
	PID                *int           `json:"pid,omitempty"`
	Details            map[string]any `json:"details,omitempty"`
	ExpectedHeartbeatS int            `json:"expected_heartbeat_s,omitempty"`
}

func (s *Server) handleCreateTask(c echo.Context) error {
	var req createTaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errBody(err))
	}
	if req.Server == "" || req.Type == "" {
		return c.JSON(http.StatusBadRequest, errBody(errors.New("server and type are required")))
	}

	t, err := s.tasks.Create(c.Request().Context(), tasks.Task{
		Server:             req.Server,
		Type:               req.Type,
		Description:        req.Description,
		PID:                req.PID,
		Details:            req.Details,
		ExpectedHeartbeatS: req.ExpectedHeartbeatS,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusCreated, t)
}

func (s *Server) handleListTasks(c echo.Context) error {
	f := tasks.ListFilter{
		Status: tasks.Status(c.QueryParam("status")),
		Server: c.QueryParam("server"),
		Type:   c.QueryParam("type"),
	}
	list, err := s.tasks.List(c.Request().Context(), f)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, list)
}

func (s *Server) handleGetTask(c echo.Context) error {
	t, err := s.tasks.Get(c.Request().Context(), c.Param("id"))
	if errors.Is(err, tasks.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, t)
}

type updateTaskRequest struct {
	Progress    *int           `json:"progress,omitempty"`
	Status      *string        `json:"status,omitempty"`
	Description *string        `json:"description,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

func (s *Server) handleUpdateTask(c echo.Context) error {
	var req updateTaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errBody(err))
	}
	var status *tasks.Status
	if req.Status != nil {
		st := tasks.Status(*req.Status)
		if !st.Valid() {
			return c.JSON(http.StatusBadRequest, errBody(fmt.Errorf("invalid status %q", *req.Status)))
		}
		status = &st
	}
	t, err := s.tasks.Update(c.Request().Context(), c.Param("id"), req.Progress, status, req.Description, req.Details)
	if errors.Is(err, tasks.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, t)
}

func (s *Server) handleHeartbeat(c echo.Context) error {
	err := s.tasks.Heartbeat(c.Request().Context(), c.Param("id"))
	if errors.Is(err, tasks.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.NoContent(http.StatusOK)
}

func (s *Server) handleCompleteTask(c echo.Context) error {
	t, err := s.tasks.Finish(c.Request().Context(), c.Param("id"), tasks.StatusCompleted, "")
	if errors.Is(err, tasks.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, t)
}

type failTaskRequest struct {
	Error string `json:"error"`
}

func (s *Server) handleFailTask(c echo.Context) error {
	var req failTaskRequest
	_ = c.Bind(&req)
	t, err := s.tasks.Finish(c.Request().Context(), c.Param("id"), tasks.StatusFailed, req.Error)
	if errors.Is(err, tasks.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, t)
}

// handleControl returns a handler that either finishes the task (cancel/
// stop are terminal) or records a pause/resume request (README §7).
func (s *Server) handleControl(action tasks.ControlAction) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")

		var t tasks.Task
		var err error
		switch action {
		case tasks.ControlCancel:
			t, err = s.tasks.Finish(ctx, id, tasks.StatusCancelled, "")
		case tasks.ControlStop:
			t, err = s.tasks.Finish(ctx, id, tasks.StatusStopped, "")
		default: // pause, resume
			t, err = s.tasks.RequestControl(ctx, id, action)
		}
		if errors.Is(err, tasks.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errBody(err))
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, errBody(err))
		}
		return c.JSON(http.StatusOK, t)
	}
}

// handleGetControl is polled by the SDK (client.Handle's control-polling
// goroutine) to learn about a cancel/stop requested through the API/CLI
// rather than by the worker's own context.
func (s *Server) handleGetControl(c echo.Context) error {
	t, err := s.tasks.Get(c.Request().Context(), c.Param("id"))
	if errors.Is(err, tasks.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errBody(err))
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	action := t.ControlAction
	if t.Status.Terminal() && action == "" {
		// A task already finished a different way (e.g. Complete()) has
		// nothing pending; report its terminal status as the action so a
		// still-polling SDK stops promptly.
		action = tasks.ControlAction(t.Status)
	}
	return c.JSON(http.StatusOK, map[string]string{"action": string(action)})
}

func (s *Server) handleTaskEvents(c echo.Context) error {
	events, err := s.tasks.Events(c.Request().Context(), c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, events)
}

package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/Mahaveer86619/LocalOps/internal/notifications"
)

// handlers for the generic, undebounced "force notify" path (README/
// design doc §13.4 extended): any caller that has already decided a
// message is worth sending posts it here and it goes straight to Slack -
// no watcher state, no threshold, no transition detection.

type notifyRequest struct {
	Message string `json:"message"`
	Level   string `json:"level,omitempty"`  // info | warning | critical, default info
	Source  string `json:"source,omitempty"` // free text, e.g. a script/watcher name
}

func (s *Server) handleNotify(c echo.Context) error {
	var req notifyRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errBody(err))
	}
	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, errBody(errors.New("message is required")))
	}
	n, err := s.notifications.Send(c.Request().Context(), req.Source, notifications.Level(req.Level), req.Message)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusCreated, n)
}

func (s *Server) handleListNotifications(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	list, err := s.notifications.List(c.Request().Context(), limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, list)
}

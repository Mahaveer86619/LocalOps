// Package server wires the REST API (README §14) on top of Echo. It is a
// thin HTTP layer: all real logic lives in internal/tasks, internal/
// watchers, internal/scheduler, internal/system.
package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Mahaveer86619/LocalOps/internal/notifications"
	"github.com/Mahaveer86619/LocalOps/internal/scheduler"
	"github.com/Mahaveer86619/LocalOps/internal/system"
	"github.com/Mahaveer86619/LocalOps/internal/tasks"
	"github.com/Mahaveer86619/LocalOps/internal/watchers"
)

type Server struct {
	Echo *echo.Echo

	tasks         *tasks.Store
	watchers      *watchers.Service
	schedules     *scheduler.Store
	notifications *notifications.Store
	diskPath      string
	startedAt     time.Time
}

func New(taskStore *tasks.Store, watcherSvc *watchers.Service, scheduleStore *scheduler.Store, notificationStore *notifications.Store, diskPath string) *Server {
	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.Logger())

	s := &Server{
		Echo:          e,
		tasks:         taskStore,
		watchers:      watcherSvc,
		schedules:     scheduleStore,
		notifications: notificationStore,
		diskPath:      diskPath,
		startedAt:     time.Now().UTC(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	e := s.Echo

	e.GET("/health", s.handleHealth)
	e.GET("/system", s.handleSystem)

	e.POST("/tasks", s.handleCreateTask)
	e.GET("/tasks", s.handleListTasks)
	e.GET("/tasks/:id", s.handleGetTask)
	e.POST("/tasks/:id/update", s.handleUpdateTask)
	e.POST("/tasks/:id/heartbeat", s.handleHeartbeat)
	e.POST("/tasks/:id/complete", s.handleCompleteTask)
	e.POST("/tasks/:id/fail", s.handleFailTask)
	e.POST("/tasks/:id/cancel", s.handleControl(tasks.ControlCancel))
	e.POST("/tasks/:id/pause", s.handleControl(tasks.ControlPause))
	e.POST("/tasks/:id/resume", s.handleControl(tasks.ControlResume))
	e.POST("/tasks/:id/stop", s.handleControl(tasks.ControlStop))
	e.GET("/tasks/:id/control", s.handleGetControl)
	e.GET("/tasks/:id/events", s.handleTaskEvents)

	e.POST("/watchers", s.handleRegisterWatcher)
	e.GET("/watchers", s.handleListWatchers)
	e.GET("/watchers/:name", s.handleGetWatcher)
	e.POST("/watchers/:name/checkin", s.handleWatcherCheckin)
	e.GET("/watchers/:name/events", s.handleWatcherEvents)

	e.POST("/schedules", s.handleCreateSchedule)
	e.GET("/schedules", s.handleListSchedules)

	e.POST("/notify", s.handleNotify)
	e.GET("/notifications", s.handleListNotifications)
}

func (s *Server) handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"status":     "ok",
		"uptime_s":   int(time.Since(s.startedAt).Seconds()),
		"started_at": s.startedAt,
	})
}

func (s *Server) handleSystem(c echo.Context) error {
	snap, err := system.Read(c.Request().Context(), s.diskPath)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, errBody(err))
	}
	return c.JSON(http.StatusOK, snap)
}

func errBody(err error) map[string]any {
	return map[string]any{"error": err.Error()}
}

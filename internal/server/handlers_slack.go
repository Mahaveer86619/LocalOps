package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/Mahaveer86619/LocalOps/internal/notifications"
	"github.com/Mahaveer86619/LocalOps/internal/slack"
	"github.com/Mahaveer86619/LocalOps/internal/system"
	"github.com/Mahaveer86619/LocalOps/internal/tasks"
)

// handleSlackCommand serves every slash command Slack is configured to
// send here (README/design doc extension: Claude/CLI/API all read state
// through the same control endpoints - a Slack slash command is just
// another read-mostly client, not a privileged shortcut). One Request URL
// in the Slack app config handles all commands; routing is on the
// "command" field Slack sends, not on the URL path.
func (s *Server) handleSlackCommand(c echo.Context) error {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.String(http.StatusBadRequest, "could not read body")
	}

	ts := c.Request().Header.Get("X-Slack-Request-Timestamp")
	sig := c.Request().Header.Get("X-Slack-Signature")
	if !slack.VerifySignature(s.slackSigningSecret, ts, sig, body) {
		return c.String(http.StatusUnauthorized, "invalid signature")
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return c.String(http.StatusBadRequest, "could not parse form body")
	}

	command := values.Get("command")
	text := strings.TrimSpace(values.Get("text"))

	reply := s.dispatchSlackCommand(c.Request().Context(), command, text)
	// response_type "ephemeral": visible only to whoever ran the command -
	// an ops status check shouldn't spam the whole channel by default.
	return c.JSON(http.StatusOK, map[string]string{
		"response_type": "ephemeral",
		"text":          reply,
	})
}

func (s *Server) dispatchSlackCommand(ctx context.Context, command, text string) string {
	switch command {
	case "/health":
		return fmt.Sprintf(":white_check_mark: LocalOps is up (uptime %ds)", int(time.Since(s.startedAt).Seconds()))
	case "/status":
		return s.slackStatusText(ctx)
	case "/tasks":
		return s.slackTasksText(ctx, text)
	case "/task":
		return s.slackTaskText(ctx, text)
	case "/watchers":
		return s.slackWatchersText(ctx)
	case "/watcher":
		return s.slackWatcherText(ctx, text)
	case "/notify":
		return s.slackNotifyText(ctx, text)
	case "":
		return "No command in request."
	default:
		return fmt.Sprintf("Unrecognized command `%s`. Available: /health /status /tasks /task /watchers /watcher /notify", command)
	}
}

func (s *Server) slackStatusText(ctx context.Context) string {
	snap, err := system.Read(ctx, s.diskPath)
	if err != nil {
		return "system: error reading system health: " + err.Error()
	}
	running, err := s.tasks.List(ctx, tasks.ListFilter{Status: tasks.StatusRunning})
	if err != nil {
		return "error listing tasks: " + err.Error()
	}
	watcherList, err := s.watchers.Store.List(ctx)
	if err != nil {
		return "error listing watchers: " + err.Error()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*SYSTEM*  cpu %.0f%%  ram %.0f%%  disk %.0f%%  uptime %ds\n", snap.CPUPercent, snap.MemPercent, snap.DiskPercent, snap.UptimeSeconds)

	fmt.Fprintf(&b, "\n*RUNNING TASKS* (%d)\n", len(running))
	if len(running) == 0 {
		b.WriteString("(none)\n")
	}
	for _, t := range running {
		fmt.Fprintf(&b, "• `%s` %d%% - %s\n", t.ID, t.Progress, t.Description)
	}

	fmt.Fprintf(&b, "\n*WATCHERS* (%d)\n", len(watcherList))
	if len(watcherList) == 0 {
		b.WriteString("(none)\n")
	}
	for _, w := range watcherList {
		fmt.Fprintf(&b, "• `%s` %s - %s\n", w.Name, strings.ToUpper(string(w.State)), w.LastMessage)
	}
	return b.String()
}

func (s *Server) slackTasksText(ctx context.Context, statusFilter string) string {
	f := tasks.ListFilter{}
	if statusFilter != "" {
		st := tasks.Status(statusFilter)
		if !st.Valid() {
			return fmt.Sprintf("Unknown status %q. Try one of: created, queued, running, paused, completed, failed, cancelled, stopped.", statusFilter)
		}
		f.Status = st
	}
	list, err := s.tasks.List(ctx, f)
	if err != nil {
		return "error: " + err.Error()
	}
	if len(list) == 0 {
		return "No tasks."
	}
	var b strings.Builder
	for _, t := range list {
		fmt.Fprintf(&b, "`%s` %-8s %-8s %3d%%  %s\n", t.ID, t.Type, t.Status, t.Progress, t.Description)
	}
	return b.String()
}

func (s *Server) slackTaskText(ctx context.Context, id string) string {
	if id == "" {
		return "Usage: /task <id>"
	}
	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("*%s*  type=%s  status=%s  progress=%d%%\n%s%s",
		t.ID, t.Type, t.Status, t.Progress, t.Description, formatTaskError(t))
}

func formatTaskError(t tasks.Task) string {
	if t.Error == "" {
		return ""
	}
	return fmt.Sprintf("\nerror: %s", t.Error)
}

func (s *Server) slackWatchersText(ctx context.Context) string {
	list, err := s.watchers.Store.List(ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	if len(list) == 0 {
		return "No watchers."
	}
	var b strings.Builder
	for _, w := range list {
		fmt.Fprintf(&b, "`%s` %-9s %-8s fails=%d  %s\n", w.Name, w.Kind, strings.ToUpper(string(w.State)), w.ConsecutiveFails, w.LastMessage)
	}
	return b.String()
}

func (s *Server) slackWatcherText(ctx context.Context, name string) string {
	if name == "" {
		return "Usage: /watcher <name>"
	}
	w, err := s.watchers.Store.Get(ctx, name)
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("*%s*  kind=%s  state=%s  fails=%d\n%s", w.Name, w.Kind, strings.ToUpper(string(w.State)), w.ConsecutiveFails, w.LastMessage)
}

func (s *Server) slackNotifyText(ctx context.Context, text string) string {
	if text == "" {
		return "Usage: /notify <message>"
	}
	if _, err := s.notifications.Send(ctx, "slack-command", notifications.LevelInfo, text); err != nil {
		return "error: " + err.Error()
	}
	return "Sent."
}

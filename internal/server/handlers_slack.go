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
	"github.com/Mahaveer86619/LocalOps/internal/watchers"
)

// handleSlackCommand serves every slash command Slack is configured to
// send here over the HTTP path (README/design doc extension: Claude/CLI/
// API/Slack all read state through the same control endpoints - a slash
// command is just another read-mostly client, not a privileged
// shortcut). One Request URL handles all commands; routing is on the
// "command" field Slack sends, not the URL path. Socket Mode
// (slack_socketmode.go) reaches the same dispatchSlackCommand.
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

const helpText = `*LocalOps commands*
` + "`/health`" + `                    quick liveness check + at-a-glance counts
` + "`/status`" + ` (or ` + "`/localops-status`" + `)  full overview: system health, running tasks, watchers
` + "`/tasks [status]`" + `           list tasks, optionally filtered (running, completed, failed, cancelled, stopped, paused, created, queued)
` + "`/task <id>`" + `                 task detail
` + "`/task <id> <action>`" + `        cancel | pause | resume | stop
` + "`/watchers`" + `                  list watchers
` + "`/watcher <name>`" + `             watcher detail
` + "`/watcher <name> events`" + `      watcher detail + recent event history
` + "`/notify <message>`" + `          force an immediate Slack message (no debounce, any content)
` + "`/notifications [n]`" + `         recent force-notify history (default 10)
` + "`/system`" + `                    system health only (cpu / ram / disk / uptime / load)
` + "`/help`" + `                      this message`

func (s *Server) dispatchSlackCommand(ctx context.Context, command, text string) string {
	switch command {
	case "/health":
		return s.slackHealthText(ctx)
	case "/status", "/localops-status":
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
	case "/notifications":
		return s.slackNotificationsText(ctx, text)
	case "/system":
		return s.slackSystemText(ctx)
	case "/help", "":
		return helpText
	default:
		return fmt.Sprintf("Unrecognized command `%s`. Try `/help` for the list.", command)
	}
}

// --- formatting helpers -----------------------------------------------

func humanAgo(t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := time.Since(*t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func humanDuration(totalSeconds int) string {
	d := time.Duration(totalSeconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh%dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm%ds", minutes, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

func gib(bytes uint64) float64 { return float64(bytes) / (1024 * 1024 * 1024) }

func taskEmoji(status tasks.Status) string {
	switch status {
	case tasks.StatusRunning:
		return "▶️" // ▶️
	case tasks.StatusCompleted:
		return "✅" // ✅
	case tasks.StatusFailed:
		return "❌" // ❌
	case tasks.StatusPaused:
		return "⏸️" // ⏸️
	case tasks.StatusCancelled, tasks.StatusStopped:
		return "\U0001f6d1" // 🛑
	default:
		return "\U0001f552" // 🕒 created/queued
	}
}

func watcherEmoji(state watchers.State) string {
	switch state {
	case watchers.StateOK:
		return "\U0001f7e2" // 🟢
	case watchers.StateDegraded:
		return "\U0001f7e1" // 🟡
	case watchers.StateDown:
		return "\U0001f534" // 🔴
	default:
		return "⚪" // ⚪
	}
}

func levelEmoji(level notifications.Level) string {
	switch level {
	case notifications.LevelWarning:
		return "⚠️" // ⚠️
	case notifications.LevelCritical:
		return "\U0001f6a8" // 🚨
	default:
		return "ℹ️" // ℹ️
	}
}

// --- command implementations -------------------------------------------

func (s *Server) slackHealthText(ctx context.Context) string {
	running, err := s.tasks.List(ctx, tasks.ListFilter{Status: tasks.StatusRunning})
	if err != nil {
		return "✅ LocalOps is up, but failed to list tasks: " + err.Error()
	}
	watcherList, err := s.watchers.Store.List(ctx)
	if err != nil {
		return "✅ LocalOps is up, but failed to list watchers: " + err.Error()
	}
	down := 0
	for _, w := range watcherList {
		if w.State == watchers.StateDown {
			down++
		}
	}
	return fmt.Sprintf("✅ *LocalOps is up* — uptime %s · %d task(s) running · %d/%d watcher(s) down",
		humanDuration(int(time.Since(s.startedAt).Seconds())), len(running), down, len(watcherList))
}

func (s *Server) slackSystemText(ctx context.Context) string {
	snap, err := system.Read(ctx, s.diskPath)
	if err != nil {
		return "error reading system health: " + err.Error()
	}
	var b strings.Builder
	b.WriteString("*System health*\n```\n")
	fmt.Fprintf(&b, "CPU     %5.1f%%\n", snap.CPUPercent)
	fmt.Fprintf(&b, "RAM     %5.1f%%  (%.1f / %.1f GiB)\n", snap.MemPercent, gib(snap.MemUsedBytes), gib(snap.MemTotalBytes))
	fmt.Fprintf(&b, "Disk    %5.1f%%  (%.1f / %.1f GiB)\n", snap.DiskPercent, gib(snap.DiskUsedBytes), gib(snap.DiskTotalBytes))
	fmt.Fprintf(&b, "Uptime  %s\n", humanDuration(int(snap.UptimeSeconds)))
	if snap.Load1 > 0 || snap.Load5 > 0 || snap.Load15 > 0 {
		fmt.Fprintf(&b, "Load    %.2f %.2f %.2f (1m 5m 15m)\n", snap.Load1, snap.Load5, snap.Load15)
	}
	b.WriteString("```")
	return b.String()
}

func (s *Server) slackStatusText(ctx context.Context) string {
	snap, err := system.Read(ctx, s.diskPath)
	if err != nil {
		return "error reading system health: " + err.Error()
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
	fmt.Fprintf(&b, "*LocalOps status* (uptime %s)\n\n", humanDuration(int(time.Since(s.startedAt).Seconds())))

	fmt.Fprintf(&b, "*System*  cpu `%.0f%%`  ram `%.0f%%`  disk `%.0f%%`\n\n", snap.CPUPercent, snap.MemPercent, snap.DiskPercent)

	fmt.Fprintf(&b, "*Running tasks* (%d)\n", len(running))
	if len(running) == 0 {
		b.WriteString("_none_\n")
	} else {
		b.WriteString("```\n")
		for _, t := range running {
			fmt.Fprintf(&b, "%-20s %3d%%  %-28s %s\n", t.ID, t.Progress, truncate(taskTitle(t), 28), truncate(t.Description, 40))
		}
		b.WriteString("```\n")
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "*Watchers* (%d)\n", len(watcherList))
	if len(watcherList) == 0 {
		b.WriteString("_none registered_\n")
	} else {
		for _, w := range watcherList {
			fmt.Fprintf(&b, "%s `%s` %s - %s _(checked %s)_\n", watcherEmoji(w.State), w.Name, strings.ToUpper(string(w.State)), w.LastMessage, humanAgo(w.LastCheckAt))
		}
	}
	return b.String()
}

// taskTitle returns the task's fixed "what is this" title if one was
// stored in details.title at creation time (see scripts/task-lib.sh),
// falling back to its type - a long-running task whose description is
// overwritten with rolling status text (e.g. "connected") would
// otherwise be indistinguishable from any other task of the same type in
// a list.
func taskTitle(t tasks.Task) string {
	if t.Details != nil {
		if title, ok := t.Details["title"].(string); ok && title != "" {
			return title
		}
	}
	return t.Type
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
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
		return "No tasks" + filterSuffix(statusFilter) + "."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*Tasks*%s (%d)\n```\n", filterSuffix(statusFilter), len(list))
	for _, t := range list {
		fmt.Fprintf(&b, "%s %-20s %-9s %3d%%  %-28s %s\n", taskEmoji(t.Status), t.ID, t.Status, t.Progress, truncate(taskTitle(t), 28), truncate(t.Description, 40))
	}
	b.WriteString("```")
	return b.String()
}

func filterSuffix(status string) string {
	if status == "" {
		return ""
	}
	return " (" + status + ")"
}

func (s *Server) slackTaskText(ctx context.Context, text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "Usage: `/task <id>` or `/task <id> <cancel|pause|resume|stop>`"
	}
	id := fields[0]
	if len(fields) >= 2 {
		return s.slackTaskControl(ctx, id, fields[1])
	}

	t, err := s.tasks.Get(ctx, id)
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s *%s*\n", taskEmoji(t.Status), t.ID)
	fmt.Fprintf(&b, "```\n")
	fmt.Fprintf(&b, "title       %s\n", taskTitle(t))
	fmt.Fprintf(&b, "server      %s\n", t.Server)
	fmt.Fprintf(&b, "type        %s\n", t.Type)
	fmt.Fprintf(&b, "status      %s\n", t.Status)
	fmt.Fprintf(&b, "progress    %d%%\n", t.Progress)
	fmt.Fprintf(&b, "description %s\n", t.Description)
	fmt.Fprintf(&b, "created     %s\n", humanAgo(&t.CreatedAt))
	fmt.Fprintf(&b, "updated     %s\n", humanAgo(&t.UpdatedAt))
	fmt.Fprintf(&b, "heartbeat   %s\n", humanAgo(t.LastHeartbeatAt))
	if t.Error != "" {
		fmt.Fprintf(&b, "error       %s\n", t.Error)
	}
	b.WriteString("```")
	return b.String()
}

func (s *Server) slackTaskControl(ctx context.Context, id, action string) string {
	var t tasks.Task
	var err error
	switch action {
	case "cancel":
		t, err = s.tasks.Finish(ctx, id, tasks.StatusCancelled, "")
	case "stop":
		t, err = s.tasks.Finish(ctx, id, tasks.StatusStopped, "")
	case "pause":
		t, err = s.tasks.RequestControl(ctx, id, tasks.ControlPause)
	case "resume":
		t, err = s.tasks.RequestControl(ctx, id, tasks.ControlResume)
	default:
		return fmt.Sprintf("Unknown action %q. Use cancel, pause, resume, or stop.", action)
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("%s `%s` → *%s*", taskEmoji(t.Status), t.ID, t.Status)
}

func (s *Server) slackWatchersText(ctx context.Context) string {
	list, err := s.watchers.Store.List(ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	if len(list) == 0 {
		return "No watchers registered."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*Watchers* (%d)\n", len(list))
	for _, w := range list {
		fmt.Fprintf(&b, "%s `%s` kind=%s fails=%d - %s _(checked %s)_\n",
			watcherEmoji(w.State), w.Name, w.Kind, w.ConsecutiveFails, w.LastMessage, humanAgo(w.LastCheckAt))
	}
	return b.String()
}

func (s *Server) slackWatcherText(ctx context.Context, text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "Usage: `/watcher <name>` or `/watcher <name> events`"
	}
	name := fields[0]
	showEvents := len(fields) >= 2 && fields[1] == "events"

	w, err := s.watchers.Store.Get(ctx, name)
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s *%s*\n```\n", watcherEmoji(w.State), w.Name)
	fmt.Fprintf(&b, "kind             %s\n", w.Kind)
	fmt.Fprintf(&b, "state            %s\n", w.State)
	fmt.Fprintf(&b, "consecutive_fails %d\n", w.ConsecutiveFails)
	fmt.Fprintf(&b, "last_message     %s\n", w.LastMessage)
	fmt.Fprintf(&b, "last_check       %s\n", humanAgo(w.LastCheckAt))
	fmt.Fprintf(&b, "last_ok          %s\n", humanAgo(w.LastOkAt))
	b.WriteString("```")

	if showEvents {
		events, err := s.watchers.Store.Events(ctx, name)
		if err != nil {
			fmt.Fprintf(&b, "\nerror listing events: %s", err.Error())
			return b.String()
		}
		if len(events) == 0 {
			b.WriteString("\n_no events_")
			return b.String()
		}
		start := 0
		if len(events) > 10 {
			start = len(events) - 10
		}
		b.WriteString("\n*Recent events*\n```\n")
		for _, e := range events[start:] {
			fmt.Fprintf(&b, "%s  %-9s %s\n", e.Timestamp.Format(time.RFC3339), e.State, e.Message)
		}
		b.WriteString("```")
	}
	return b.String()
}

func (s *Server) slackNotifyText(ctx context.Context, text string) string {
	if text == "" {
		return "Usage: `/notify <message>`"
	}
	if _, err := s.notifications.Send(ctx, "slack-command", notifications.LevelInfo, text); err != nil {
		return "error: " + err.Error()
	}
	return "✅ Sent."
}

func (s *Server) slackNotificationsText(ctx context.Context, text string) string {
	limit := 10
	if text != "" {
		if n, err := parsePositiveInt(text); err == nil {
			limit = n
		}
	}
	list, err := s.notifications.List(ctx, limit)
	if err != nil {
		return "error: " + err.Error()
	}
	if len(list) == 0 {
		return "No notifications yet."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*Recent notifications* (%d)\n", len(list))
	for _, n := range list {
		src := n.Source
		if src == "" {
			src = "-"
		}
		fmt.Fprintf(&b, "%s _%s_ [%s] %s\n", levelEmoji(n.Level), humanAgo(&n.CreatedAt), src, n.Message)
	}
	return b.String()
}

func parsePositiveInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return n, nil
}

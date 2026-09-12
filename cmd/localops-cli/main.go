// Command localops-cli is the lightweight operator CLI (README §15),
// meant for SSH/tmux/Claude Code usage: it talks to a running localops
// server over plain HTTP, nothing more.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func baseURL() string {
	if v := os.Getenv("LOCALOPS_URL"); v != "" {
		return v
	}
	return "http://localhost:7717"
}

var httpClient = &http.Client{Timeout: 5 * time.Second}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "status":
		err = cmdStatus()
	case "tasks":
		filter := ""
		if len(args) > 1 {
			filter = args[1]
		}
		err = cmdTasks(filter)
	case "task":
		if len(args) < 2 {
			usage()
			os.Exit(1)
		}
		if len(args) >= 3 {
			err = cmdTaskControl(args[1], args[2])
		} else {
			err = cmdTask(args[1])
		}
	case "watchers":
		err = cmdWatchers()
	case "watcher":
		if len(args) < 2 {
			usage()
			os.Exit(1)
		}
		if len(args) >= 3 && args[2] == "events" {
			err = cmdWatcherEvents(args[1])
		} else {
			err = cmdWatcher(args[1])
		}
	case "system":
		err = cmdSystem()
	case "health":
		err = cmdHealth()
	case "notify":
		if len(args) < 2 {
			usage()
			os.Exit(1)
		}
		level, source := "info", "cli"
		if len(args) > 2 {
			level = args[2]
		}
		if len(args) > 3 {
			source = args[3]
		}
		err = cmdNotify(args[1], level, source)
	case "notifications":
		err = cmdNotifications()
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`localops-cli - LocalOps operator CLI

Usage:
  localops-cli status
  localops-cli tasks [running|failed|completed|cancelled|stopped|paused]
  localops-cli task <id>
  localops-cli task <id> <cancel|pause|resume|stop>
  localops-cli watchers
  localops-cli watcher <name>
  localops-cli watcher <name> events
  localops-cli system
  localops-cli health
  localops-cli notify "<message>" [level] [source]   # force an immediate Slack message, no debounce
  localops-cli notifications                          # recent force-notify history

Set LOCALOPS_URL to point at a non-default server (default http://localhost:7717).`)
}

func get(path string, out any) error {
	resp, err := httpClient.Get(baseURL() + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s -> %s: %s", path, resp.Status, string(body))
	}
	return json.Unmarshal(body, out)
}

func post(path string, out any) error {
	return postBody(path, "{}", out)
}

func postBody(path, jsonBody string, out any) error {
	resp, err := httpClient.Post(baseURL()+path, "application/json", strings.NewReader(jsonBody))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s -> %s: %s", path, resp.Status, string(body))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

type task struct {
	ID          string         `json:"id"`
	Server      string         `json:"server"`
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	Progress    int            `json:"progress"`
	Description string         `json:"description"`
	Details     map[string]any `json:"details,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// title returns the task's fixed "what is this" title if one was stored
// in details.title at creation (see scripts/task-lib.sh), falling back
// to its type - a long-running task whose description is overwritten
// with rolling status text (e.g. "connected") would otherwise be
// indistinguishable from any other task of the same type in a list.
func (t task) title() string {
	if v, ok := t.Details["title"].(string); ok && v != "" {
		return v
	}
	return t.Type
}

type watcher struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	State            string `json:"state"`
	LastMessage      string `json:"last_message"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	LastCheckAt      string `json:"last_check_at"`
}

func cmdStatus() error {
	var health map[string]any
	if err := get("/health", &health); err != nil {
		return err
	}
	var sys map[string]any
	if err := get("/system", &sys); err != nil {
		return err
	}
	var running []task
	if err := get("/tasks?status=running", &running); err != nil {
		return err
	}
	var ws []watcher
	if err := get("/watchers", &ws); err != nil {
		return err
	}

	fmt.Println("SYSTEM")
	fmt.Println(strings.Repeat("-", 24))
	fmt.Printf("CPU     %.0f%%\n", toFloat(sys["cpu_percent"]))
	fmt.Printf("RAM     %.0f%%\n", toFloat(sys["mem_percent"]))
	fmt.Printf("Disk    %.0f%%\n", toFloat(sys["disk_percent"]))
	fmt.Printf("Uptime  %ds\n", int(toFloat(sys["uptime_seconds"])))

	fmt.Println()
	fmt.Println("RUNNING TASKS")
	fmt.Println(strings.Repeat("-", 24))
	if len(running) == 0 {
		fmt.Println("(none)")
	}
	for _, t := range running {
		fmt.Printf("* %-20s %3d%%  %-28s %s\n", t.ID, t.Progress, t.title(), t.Description)
	}

	fmt.Println()
	fmt.Println("WATCHERS")
	fmt.Println(strings.Repeat("-", 24))
	if len(ws) == 0 {
		fmt.Println("(none)")
	}
	for _, w := range ws {
		fmt.Printf("* %-24s %-9s %s\n", w.Name, strings.ToUpper(w.State), w.LastMessage)
	}
	return nil
}

func toFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func cmdTasks(filter string) error {
	path := "/tasks"
	if filter != "" {
		path += "?status=" + filter
	}
	var list []task
	if err := get(path, &list); err != nil {
		return err
	}
	for _, t := range list {
		fmt.Printf("%-20s %-9s %3d%%  %-28s %s\n", t.ID, t.Status, t.Progress, t.title(), t.Description)
	}
	return nil
}

func cmdTask(id string) error {
	var t task
	if err := get("/tasks/"+id, &t); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdTaskControl(id, action string) error {
	switch action {
	case "cancel", "pause", "resume", "stop":
	default:
		return fmt.Errorf("unknown control action %q", action)
	}
	var t task
	if err := post("/tasks/"+id+"/"+action, &t); err != nil {
		return err
	}
	fmt.Printf("task %s -> %s\n", t.ID, t.Status)
	return nil
}

func cmdWatchers() error {
	var ws []watcher
	if err := get("/watchers", &ws); err != nil {
		return err
	}
	for _, w := range ws {
		fmt.Printf("%-24s %-9s %-9s fails=%d  %s\n", w.Name, w.Kind, strings.ToUpper(w.State), w.ConsecutiveFails, w.LastMessage)
	}
	return nil
}

func cmdWatcher(name string) error {
	var w watcher
	if err := get("/watchers/"+name, &w); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(w, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdWatcherEvents(name string) error {
	var events []map[string]any
	if err := get("/watchers/"+name+"/events", &events); err != nil {
		return err
	}
	for _, e := range events {
		fmt.Printf("%s  %-9s %s\n", e["timestamp"], e["state"], e["message"])
	}
	return nil
}

func cmdSystem() error {
	var sys map[string]any
	if err := get("/system", &sys); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(sys, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdHealth() error {
	var health map[string]any
	if err := get("/health", &health); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(health, "", "  ")
	fmt.Println(string(b))
	return nil
}

func cmdNotify(message, level, source string) error {
	payload, _ := json.Marshal(map[string]string{"message": message, "level": level, "source": source})
	var n map[string]any
	if err := postBody("/notify", string(payload), &n); err != nil {
		return err
	}
	fmt.Printf("sent (id=%v)\n", n["id"])
	return nil
}

func cmdNotifications() error {
	var list []map[string]any
	if err := get("/notifications", &list); err != nil {
		return err
	}
	for _, n := range list {
		src := ""
		if s, ok := n["source"].(string); ok && s != "" {
			src = " [" + s + "]"
		}
		fmt.Printf("%s  %-9s%s  %s\n", n["created_at"], n["level"], src, n["message"])
	}
	return nil
}

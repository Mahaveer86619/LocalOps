// Command localops runs the LocalOps server: REST API, stuck-task sweep,
// and the (minimal) recurring-task scheduler.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mahaveer86619/LocalOps/internal/config"
	"github.com/Mahaveer86619/LocalOps/internal/health"
	"github.com/Mahaveer86619/LocalOps/internal/notifications"
	"github.com/Mahaveer86619/LocalOps/internal/notify"
	"github.com/Mahaveer86619/LocalOps/internal/scheduler"
	"github.com/Mahaveer86619/LocalOps/internal/server"
	"github.com/Mahaveer86619/LocalOps/internal/storage"
	"github.com/Mahaveer86619/LocalOps/internal/tasks"
	"github.com/Mahaveer86619/LocalOps/internal/watchers"
)

func main() {
	if err := config.LoadEnvFile(""); err != nil {
		log.Printf("localops: load .env: %v", err)
	}
	cfg := config.Load()

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("localops: open database: %v", err)
	}
	defer db.Close()

	taskStore := tasks.NewStore(db)
	watcherStore := watchers.NewStore(db)
	scheduleStore := scheduler.NewStore(db)

	slack := notify.New(cfg.SlackWebhookURL)
	watcherSvc := watchers.NewService(watcherStore, slack, cfg.WatcherFailThreshold)
	notificationStore := notifications.NewStore(db, slack)

	diskPath := "/"
	if os.PathSeparator == '\\' {
		diskPath = "C:"
	}
	srv := server.New(server.Options{
		Tasks:              taskStore,
		Watchers:           watcherSvc,
		Schedules:          scheduleStore,
		Notifications:      notificationStore,
		DiskPath:           diskPath,
		SlackSigningSecret: cfg.SlackSigningSecret,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor := health.NewMonitor(taskStore, slack, cfg.StuckMultiplier, time.Duration(cfg.SweepIntervalS)*time.Second)
	go monitor.Run(ctx)

	if cfg.SlackAppToken != "" {
		socketClient := server.NewSocketModeClient(cfg.SlackAppToken, srv)
		go socketClient.Run(ctx)
	}

	runner := scheduler.NewRunner(scheduleStore, taskStore, time.Duration(cfg.SweepIntervalS)*time.Second)
	go runner.Run(ctx)

	go func() {
		addr := ":" + cfg.Port
		log.Printf("localops: listening on %s (db=%s, slack_alerts=%v, slack_commands_http=%v, slack_commands_socket=%v)", addr, cfg.DBPath, cfg.SlackWebhookURL != "", cfg.SlackSigningSecret != "", cfg.SlackAppToken != "")
		if err := srv.Echo.Start(addr); err != nil {
			log.Printf("localops: server stopped: %v", err)
			cancel()
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
		log.Println("localops: shutting down")
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Echo.Shutdown(shutdownCtx); err != nil {
		log.Printf("localops: shutdown: %v", err)
	}
}

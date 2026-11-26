package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mgts-pulse/internal/pulse"
	"mgts-pulse/web"
)

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func main() {
	if err := run(); err != nil {
		slog.Error("stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	command := flag.String("command", "serve", "serve | migrate | demo | import | user")
	dbPath := flag.String("db", env("DB_PATH", "data/pulse.db"), "SQLite path")
	intern := flag.String("intern", "", "intern XLSX path")
	mentor := flag.String("mentor", "", "mentor XLSX path")
	history := flag.String("history", "data/history.json", "normalized demo history")
	username := flag.String("username", "", "new user login")
	role := flag.String("role", "", "hr | mentor | intern")
	subject := flag.String("subject", "", "intern or mentor ID")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0700); err != nil {
		return err
	}
	store, err := pulse.Open(*dbPath)
	if err != nil {
		return err
	}
	defer store.DB.Close()
	switch *command {
	case "migrate":
		return store.Migrate(ctx)
	case "demo":
		if err = store.Migrate(ctx); err != nil {
			return err
		}
		var n int
		if err = store.DB.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if err = store.DemoUsers(ctx); err != nil {
				return err
			}
		}
		b, err := os.ReadFile(*history)
		if err != nil {
			return err
		}
		var rs []pulse.Response
		if err = json.Unmarshal(b, &rs); err != nil {
			return err
		}
		created, err := store.Import(ctx, rs)
		if err != nil {
			return err
		}
		slog.Info("demo imported", "created", created)
		return nil
	case "user":
		return store.AddUser(ctx, *username, os.Getenv("USER_PASSWORD"), *role, *subject)
	case "import":
		if *intern == "" || *mentor == "" {
			return errors.New("both -intern and -mentor XLSX paths are required")
		}
		a, err := pulse.ReadExcel(*intern, "intern")
		if err != nil {
			return err
		}
		b, err := pulse.ReadExcel(*mentor, "mentor")
		if err != nil {
			return err
		}
		n, err := store.Import(ctx, append(a, b...))
		slog.Info("history import", "created", n)
		return err
	case "serve":
	default:
		return errors.New("unknown command")
	}
	if err = store.Ready(ctx); err != nil {
		return fmt.Errorf("run -command migrate or -command demo first: %w", err)
	}
	if err = store.UpgradeHRTasks(ctx); err != nil {
		return err
	}
	hrTasks, err := pulse.NewHRTaskClient(os.Getenv("HR_TASK_URL"), os.Getenv("HR_TASK_TOKEN"))
	if err != nil {
		return err
	}
	huntflow, err := pulse.NewHuntflowClient(os.Getenv("HUNTFLOW_API_URL"), os.Getenv("HUNTFLOW_ACCOUNT_ID"),
		os.Getenv("HUNTFLOW_ACCESS_TOKEN"), os.Getenv("HUNTFLOW_APPLICANT_MAP"))
	if err != nil {
		return err
	}
	agentURL := os.Getenv("AGENT_URL")
	agentToken := os.Getenv("AGENT_TOKEN")
	if agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid AGENT_URL")
		}
		if len(agentToken) < 24 {
			return errors.New("AGENT_TOKEN must be at least 24 characters")
		}
	}
	for _, name := range []string{"WEBHOOK_SECRET", "TALLY_SIGNING_SECRET"} {
		if v := os.Getenv(name); v != "" && len(v) < 24 {
			return fmt.Errorf("%s must be at least 24 characters", name)
		}
	}
	if a, b := os.Getenv("TALLY_INTERN_FORM_ID"), os.Getenv("TALLY_MENTOR_FORM_ID"); a != "" && a == b {
		return errors.New("Tally form IDs must differ")
	}
	secure := strings.EqualFold(os.Getenv("COOKIE_SECURE"), "true")
	app := &pulse.Server{Store: store, AgentURL: agentURL, WebhookSecret: os.Getenv("WEBHOOK_SECRET"), TallySecret: os.Getenv("TALLY_SIGNING_SECRET"), InternFormID: os.Getenv("TALLY_INTERN_FORM_ID"), MentorFormID: os.Getenv("TALLY_MENTOR_FORM_ID"), InternFormURL: os.Getenv("INTERN_FORM_URL"), MentorFormURL: os.Getenv("MENTOR_FORM_URL"), SecureCookie: secure, HRTasks: hrTasks, Huntflow: huntflow}
	worker := &pulse.Worker{Store: store, URL: agentURL, Token: agentToken}
	// Startup reconciliation also recovers committed responses after a crash before queuing.
	if agentURL != "" {
		people, err := store.People(ctx)
		if err != nil {
			return err
		}
		for _, p := range people {
			if _, err = store.Queue(ctx, p.ID); err != nil && !errors.Is(err, pulse.ErrNotFound) {
				return err
			}
		}
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.Run(workerCtx) }()
	srv := &http.Server{Addr: env("ADDR", "127.0.0.1:8080"), Handler: app.Handler(web.Files), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 16}
	errc := make(chan error, 1)
	go func() { slog.Info("listening", "url", "http://"+srv.Addr); errc <- srv.ListenAndServe() }()
	select {
	case err = <-errc:
		cancelWorker()
		<-workerDone
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdown)
	cancelWorker()
	<-workerDone
	return err
}

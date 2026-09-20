// Command herdr-mobile-gateway serves Herdr-managed Claude Code / Codex
// sessions to the Herdr Companion Android app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/agentupdate"
	"github.com/tohutohu/herdr-android-client/gateway/internal/api"
	"github.com/tohutohu/herdr-android-client/gateway/internal/archive"
	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/directorycheck"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/logging"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/claude"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/codex"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/opencode"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
	"github.com/tohutohu/herdr-android-client/gateway/internal/usage"
)

const helpText = `herdr-mobile-gateway — Herdr Companion gateway

Usage:
  herdr-mobile-gateway [serve] [flags]     run the gateway (default)
  herdr-mobile-gateway token [--rotate]    print (or rotate) the auth token
  herdr-mobile-gateway devices             list registered FCM devices
  herdr-mobile-gateway usage               print the current subscription limits
  herdr-mobile-gateway notify-test         send a test push to every registered device
  herdr-mobile-gateway import-firebase --service-account FILE --android-config FILE
                                          import Firebase files while gateway is stopped
  herdr-mobile-gateway set-jev-key         read optional Jev key from stdin (empty removes it)
  herdr-mobile-gateway debug replay FILE   re-parse dead-letter entries with the current adapters

Environment:
  HERDR_MOBILE_CONFIG      config file (default ~/.config/herdr-mobile/config.json)
  HERDR_MOBILE_STATE_DIR   state dir for logs and dead letters (default ~/.local/state/herdr-mobile)
  HERDR_MOBILE_FCM_CREDENTIALS  Firebase service account JSON
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "set-jev-key":
		err = setJevKeyCmd()
	case "import-firebase":
		err = importFirebaseCmd(args)
	case "serve":
		err = serve(args)
	case "token":
		err = tokenCmd(args)
	case "devices":
		err = devicesCmd()
	case "usage":
		err = usageCmd()
	case "notify-test":
		err = notifyTestCmd()
	case "debug":
		err = debugCmd(args)
	case "help", "-h", "--help":
		fmt.Print(helpText)
	default:
		fmt.Fprint(os.Stderr, helpText)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "", "listen address (overrides config), e.g. 100.x.y.z:8765 or 192.168.1.20:8765")
	debug := fs.Bool("debug", false, "debug logging")
	parentPID := fs.Int("parent-pid", 0, "exit when the owning desktop process exits")
	logFile := fs.String("log-file", filepath.Join(config.StateDir(), "gateway.log"), "log file (empty: stdout only)")
	fs.Parse(args)

	closer, err := logging.Setup(*logFile, *debug)
	if err != nil {
		return err
	}
	defer closer.Close()

	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	cfg := store.Get()
	if *listen != "" {
		cfg.Listen = *listen
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *parentPID > 0 {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if os.Getppid() != *parentPID {
						stop()
						return
					}
				}
			}
		}()
	}

	sink := deadletter.NewWriter(filepath.Join(config.StateDir(), "errors"))
	hc := herdr.New(cfg.HerdrSocket)
	up := uploads.New(cfg.UploadDir, 24*time.Hour)
	go up.RunCleanup(ctx, time.Hour)

	claudeProvider := claude.New(cfg.ClaudeConfigDir, hc, sink)
	codexProvider := codex.New(cfg.CodexBinary, cfg.CodexDaemonSock, hc, sink)
	go codexProvider.Run(ctx)
	openCodeProvider := opencode.New(cfg.OpenCode, hc, sink)
	svc := sessions.New(hc, time.Duration(cfg.OfflineSessionDays)*24*time.Hour, claudeProvider, codexProvider, openCodeProvider)
	archived, err := archive.Open(filepath.Join(config.StateDir(), "archive.json"))
	if err != nil {
		return err
	}
	svc.Archive = archived

	roots := cfg.WorkspaceRoots
	if len(roots) == 0 {
		roots = launcher.DefaultRoots()
	}
	launch := &launcher.Launcher{Herdr: hc, Roots: roots, Providers: []providers.Provider{claudeProvider, codexProvider, openCodeProvider}}

	jevKey := os.Getenv("TYPESAFE_API_KEY")
	if jevKey == "" {
		jevKey = cfg.JevAPIKey
	}
	dirCheck := &directorycheck.Checker{APIKey: jevKey, Providers: []directorycheck.History{claudeProvider, codexProvider}}

	limits := usage.New(cfg.UsageCommand, time.Duration(cfg.UsageRefreshMinutes)*time.Minute, codexProvider)
	go limits.Run(ctx)

	watcher := &notifications.Watcher{Herdr: hc, Sessions: svc, Config: store, Sink: sink}
	if sender, err := newSender(cfg); err != nil {
		slog.Warn("push notifications disabled", "operation", "fcm", "error", err)
	} else {
		watcher.Sender = sender
	}
	go watcher.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           (&api.Server{AgentUpdates: agentupdate.New(ctx, cfg.CodexBinary, cfg.OpenCode.Binary), Sessions: svc, Terminal: hc, Uploads: up, Config: store, Sink: sink, Launcher: launch, Archive: archived, Usage: limits, DirectoryCheck: dirCheck}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	slog.Info("gateway listening", "operation", "serve", "addr", cfg.Listen, "herdr_socket", hc.Socket(), "config", store.Path())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func tokenCmd(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	rotate := fs.Bool("rotate", false, "generate a new token")
	fs.Parse(args)
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	if *rotate {
		if err := store.Update(func(c *config.Config) { c.AuthToken = config.NewToken() }); err != nil {
			return err
		}
	}
	fmt.Println(store.Get().AuthToken)
	return nil
}

func devicesCmd() error {
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	for _, d := range store.Get().Devices {
		fmt.Printf("%s\t%s\t%s…\n", d.Name, d.RegisteredAt.Format(time.RFC3339), d.FCMToken[:min(12, len(d.FCMToken))])
	}
	return nil
}

// usageCmd reads the limits once, for checking that the reporter is set up.
func usageCmd() error {
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	cfg := store.Get()
	limits := usage.New(cfg.UsageCommand, time.Duration(cfg.UsageRefreshMinutes)*time.Minute)
	snap := limits.Refresh(context.Background())
	if snap.Error != "" {
		return errors.New(snap.Error)
	}
	for _, p := range snap.Providers {
		if p.Error != "" {
			fmt.Printf("%s\t%s\n", p.DisplayName, p.Error)
			continue
		}
		for _, w := range p.Windows {
			label := w.Label
			if w.Scope != "" {
				label += " (" + w.Scope + ")"
			}
			resets := ""
			if w.ResetsAt != nil {
				resets = "\tresets " + w.ResetsAt.Local().Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%d%% used%s\n", p.DisplayName, label, w.UsedPercent, resets)
		}
	}
	return nil
}

func newSender(cfg config.Config) (*notifications.FCM, error) {
	path := notifications.CredentialsPath(cfg.FCMCredentialsFile)
	if path == "" {
		return nil, errors.New("no Firebase service account (set HERDR_MOBILE_FCM_CREDENTIALS or fcmCredentialsFile)")
	}
	return notifications.NewFCM(path)
}

func notifyTestCmd() error {
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	sender, err := newSender(store.Get())
	if err != nil {
		return err
	}
	devices := store.Get().Devices
	if len(devices) == 0 {
		return errors.New("no devices registered; open the app and save the gateway settings first")
	}
	data := map[string]string{
		"gatewayId": config.GatewayID(store.Get().AuthToken),
		"sessionId": "test:notification",
		"status":    "completed",
		"title":     "Herdr Companion test",
		"body":      "Push notifications are working.",
	}
	for _, d := range devices {
		if err := sender.Send(context.Background(), d.FCMToken, data); err != nil {
			fmt.Printf("%s: %v\n", d.Name, err)
		} else {
			fmt.Printf("%s: sent\n", d.Name)
		}
	}
	return nil
}

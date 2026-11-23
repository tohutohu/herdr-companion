// Command herdr-mobile-gateway is the Herdr Companion Gateway. It runs
// Claude Code and Codex sessions itself (Claude Agent SDK sidecar, its own
// Codex app-server) and serves them to the Herdr Companion apps.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/agentplugin"
	"github.com/tohutohu/herdr-android-client/gateway/internal/agentupdate"
	"github.com/tohutohu/herdr-android-client/gateway/internal/api"
	"github.com/tohutohu/herdr-android-client/gateway/internal/archive"
	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/directorycheck"
	"github.com/tohutohu/herdr-android-client/gateway/internal/events"
	"github.com/tohutohu/herdr-android-client/gateway/internal/favorites"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/logging"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notes"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
	"github.com/tohutohu/herdr-android-client/gateway/internal/projects"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/claude"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/codex"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/dsh"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
	"github.com/tohutohu/herdr-android-client/gateway/internal/search"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/shellenv"
	"github.com/tohutohu/herdr-android-client/gateway/internal/translation"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
	"github.com/tohutohu/herdr-android-client/gateway/internal/usage"
	"github.com/tohutohu/herdr-android-client/gateway/internal/worktree"
)

const helpText = `herdr-mobile-gateway — Herdr Companion Gateway

Usage:
  herdr-mobile-gateway [serve] [flags]     run the gateway (default)
  herdr-mobile-gateway token [--rotate]    print (or rotate) the auth token
  herdr-mobile-gateway devices             list registered FCM devices
  herdr-mobile-gateway usage               print the current subscription limits
  herdr-mobile-gateway notify-test         send a test push to every registered device
  herdr-mobile-gateway import-firebase --service-account FILE --android-config FILE
                                          import Firebase files while gateway is stopped
  herdr-mobile-gateway set-jev-key         read optional Jev key from stdin (empty removes it)
  herdr-mobile-gateway set-history-cache MB  memory per agent for conversation histories (0 = default 2048)
  herdr-mobile-gateway dsh status          print the DeepSeek Harness executable, version, keyed routes and models (JSON)
  herdr-mobile-gateway dsh configure       apply a JSON change from stdin: {"binary","keys","models","defaultModel"}
  herdr-mobile-gateway debug replay FILE   re-parse dead-letter entries with the current adapters

Environment:
  HERDR_MOBILE_CONFIG      config file (default ~/.config/herdr-mobile/config.json)
  HERDR_MOBILE_STATE_DIR   state dir for logs and dead letters (default ~/.local/state/herdr-mobile)
  HERDR_MOBILE_FCM_CREDENTIALS  Firebase service account JSON
  HERDR_CLAUDE_SIDECAR     herdr-claude-sidecar executable (default: next to this one, then PATH)
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
	case "set-history-cache":
		err = setHistoryCacheCmd(args)
	case "dsh":
		err = dshCmd(args)
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
	listen := fs.String("listen", "", "listen address (overrides config), e.g. 100.x.y.z:8765, 192.168.1.20:8765, or gateway.example.com:8765")
	debug := fs.Bool("debug", false, "debug logging")
	parentPID := fs.Int("parent-pid", 0, "exit when the owning desktop process exits")
	logFile := fs.String("log-file", filepath.Join(config.StateDir(), "gateway.log"), "log file (empty: stdout only)")
	fs.Parse(args)

	closer, err := logging.Setup(*logFile, *debug)
	if err != nil {
		return err
	}
	defer closer.Close()
	// Before anything looks up or starts a tool: the menu-bar app starts the
	// gateway with launchd's short PATH, not the terminal's.
	shellenv.Apply(context.Background())

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
	up := uploads.New(cfg.UploadDir, 24*time.Hour)
	go up.RunCleanup(ctx, time.Hour)

	claudeProvider := claude.New(cfg.ClaudeConfigDir, sink)
	codexProvider := codex.New(cfg.CodexBinary, sink)
	dshProvider, err := dsh.New(dshConfig(cfg), filepath.Join(config.StateDir(), "dsh"), sink)
	if err != nil {
		return fmt.Errorf("open DSH sessions: %w", err)
	}
	defer dshProvider.Close()
	// Every provider the Gateway runs agents with. As a DirectRuntime each
	// gives its agents the Gateway's skills (SetAgentPlugin).
	agents := []agentRuntime{claudeProvider, codexProvider, dshProvider}
	claudeProvider.SetCacheBytes(int64(cfg.HistoryCacheMB) << 20)
	claudeProvider.SetDirect(claude.DirectConfig{Sidecar: sidecarPath(cfg.ClaudeSidecar), Claude: claudePath(cfg.ClaudeBinary), AllowAPIKey: cfg.ClaudeAllowAPIKey})
	// The Gateway's own skills, the same for every agent it runs, in sessions
	// and resident runs. Without them the agents still run.
	plugin, err := agentplugin.Install(filepath.Join(config.StateDir(), "agent-plugin"))
	if err != nil {
		slog.Warn("agent skills are not installed", "operation", "agent_plugin.install", "error", err)
	} else {
		for _, rt := range agents {
			rt.SetAgentPlugin(plugin)
		}
	}
	codexProvider.SetCacheBytes(cfg.HistoryCacheMB << 20)
	go codexProvider.Run(ctx)
	svc := sessions.New(time.Duration(cfg.OfflineSessionDays)*24*time.Hour, claudeProvider, codexProvider, dshProvider)
	archived, err := archive.Open(filepath.Join(config.StateDir(), "archive.json"))
	if err != nil {
		return err
	}
	svc.Archive = archived
	sessionNotes, err := notes.Open(filepath.Join(config.StateDir(), "notes.json"))
	if err != nil {
		return err
	}
	favoriteDirs, err := favorites.Open(filepath.Join(config.StateDir(), "favorites.json"))
	if err != nil {
		return err
	}
	// Projects are the favorite folders: they group sessions and resident
	// tasks and can turn their notifications down.
	projectOf := &projects.Resolver{Store: favoriteDirs, MainCheckout: worktree.MainCheckout}
	svc.Annotate = projectOf.Annotate
	offlineWindow := time.Duration(cfg.OfflineSessionDays) * 24 * time.Hour
	direct, err := runtime.New(filepath.Join(config.StateDir(), "direct-sessions.json"), offlineWindow, agentProviders(agents)...)
	if err != nil {
		return err
	}
	svc.Direct = direct
	delivery, err := api.OpenMessageDeliveryStore(filepath.Join(config.StateDir(), "message-delivery"))
	if err != nil {
		return err
	}

	roots := cfg.WorkspaceRoots
	if len(roots) == 0 {
		roots = launcher.DefaultRoots()
	}
	launch := &launcher.Launcher{Roots: roots, Providers: []providers.Provider{claudeProvider, codexProvider, dshProvider},
		Direct: direct, WorktreeDir: filepath.Join(config.StateDir(), "worktrees")}

	jevKey := os.Getenv("TYPESAFE_API_KEY")
	if jevKey == "" {
		jevKey = cfg.JevAPIKey
	}
	dirCheck := &directorycheck.Checker{APIKey: jevKey, Providers: []directorycheck.History{claudeProvider, codexProvider, dshProvider}}

	limits := usage.New(cfg.UsageCommand, time.Duration(cfg.UsageRefreshMinutes)*time.Minute, codexProvider).
		WithOpenCodeGoKey(func() string { return openCodeGoKey(store.Get()) })
	go limits.Run(ctx)

	translator := translation.New(ctx, filepath.Join(config.StateDir(), "translation"), svc)
	defer translator.Close()
	prepareNotification := func(ctx context.Context, id string, content notifications.Content) notifications.Content {
		return translator.Notification(ctx, id, content, direct.Translation(id))
	}
	watcher := &notifications.Watcher{Sessions: svc, Conversations: svc, Config: store, Sink: sink, Prepare: prepareNotification}
	watcher.Observe = func(ctx context.Context, session model.Session) {
		mode := direct.Translation(session.ID)
		if !translator.OutputEnabled(mode) || session.Status != model.StatusRunning {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, msgs, err := svc.Conversation(ctx, session.ID); err == nil {
			translator.Messages(session.ID, msgs, false, mode)
		}
	}
	var sender notifications.Sender
	if fcm, err := newSender(cfg); err != nil {
		slog.Warn("push notifications disabled", "operation", "fcm", "error", err)
	} else {
		sender = fcm
		watcher.Sender = fcm
	}
	// Change hints for connected apps (GET /v1/events).
	hub := events.New()
	tracker := &events.Tracker{Hub: hub, Live: svc.LiveSessions}
	streamer := &events.Streamer{Hub: hub}
	for _, p := range []providers.Provider{claudeProvider, codexProvider, dshProvider} {
		if r, ok := p.(providers.StreamReporter); ok {
			name := p.Name()
			r.SetStreamListener(func(rep providers.StreamReport) {
				streamer.Report(events.Streaming{SessionID: name + ":" + rep.NativeID, MessageID: rep.MessageID, Text: rep.Text, Done: rep.Done})
			})
		}
	}
	direct.SetListener(func() {
		watcher.Kick()
		tracker.Kick()
	})
	go watcher.Run(ctx)
	go tracker.Run(ctx)

	// Resident tasks: their own database, sidecar, app-server and DSH SDK; nothing
	// is shared with the session runtime or its watcher but the FCM sender.
	residentStore, err := resident.Open(filepath.Join(config.StateDir(), "resident.db"))
	if err != nil {
		return fmt.Errorf("open resident tasks: %w", err)
	}
	defer residentStore.Close()
	claudeResident, codexResident := claudeProvider.ResidentRunner(), codexProvider.ResidentRunner()
	residents := resident.New(residentStore, residentEnv{launch: launch, providers: []providers.Provider{claudeProvider, codexProvider, dshProvider}},
		map[string]resident.Runner{resident.ProviderClaude: claudeResident, resident.ProviderCodex: codexResident, resident.ProviderDSH: dshProvider.ResidentRunner()},
		resident.Config{MaxConcurrentRuns: cfg.ResidentMaxConcurrentRuns, CatchUpWindow: time.Duration(cfg.ResidentCatchUpHours) * time.Hour}, nil)
	residents.OnChange = func() { hub.Publish(events.TypeResident, struct{}{}) }
	residents.Notifier = &resident.Notifier{Store: residentStore, Sender: sender, Config: store, Prepare: prepareNotification,
		Wants: func(cwd, kind string) bool {
			level := ""
			if p, ok := projectOf.Of(cwd); ok {
				level = p.Notify
			}
			return projects.Wants(level, kind != resident.NotifyChange)
		}}
	if err := residents.Recover(ctx); err != nil {
		return fmt.Errorf("recover resident runs: %w", err)
	}
	svc.Hidden = residents.HiddenSessionIDs
	residentDone := make(chan struct{})
	go func() { residents.Run(ctx); close(residentDone) }()

	// The search index is a cache; without it only /v1/sessions/search is missing.
	searchIndex, err := search.Open(ctx, filepath.Join(config.StateDir(), "search.db"), svc)
	if err != nil {
		slog.Warn("search index unavailable", "operation", "serve", "error", err)
		searchIndex = nil
	} else {
		defer searchIndex.Close()
		// Read what is new since the last run before the first search asks.
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(15 * time.Second):
				searchIndex.Refresh()
			}
		}()
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           (&api.Server{Search: searchIndex, Translation: translator, MessageDelivery: delivery, AgentUpdates: agentupdate.New(ctx, cfg.CodexBinary), Sessions: svc, Uploads: up, Config: store, Sink: sink, Launcher: launch, Archive: archived, Usage: limits, DirectoryCheck: dirCheck, Direct: direct, Resident: residents, Notes: sessionNotes, Favorites: favoriteDirs, Projects: projectOf, Listen: cfg.Listen, Events: hub, Kick: tracker.Kick}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Event streams never end on their own; Shutdown waits for them.
	srv.RegisterOnShutdown(hub.Close)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
		// Record which direct sessions were cut off, then end their
		// processes (the Codex app-server ends with ctx).
		direct.Shutdown()
		claudeProvider.Close()
		dshProvider.Close()
		// Resident runs cut off here are decided on the next start.
		<-residentDone
		residents.Shutdown(15 * time.Second)
		claudeResident.Close()
		codexResident.Close()
	}()
	slog.Info("gateway listening", "operation", "serve", "addr", cfg.Listen, "config", store.Path(),
		"claude_sidecar", sidecarPath(cfg.ClaudeSidecar), "claude", claudePath(cfg.ClaudeBinary), "dsh", dsh.FindBinary(cfg.DSHBinary))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Let the shutdown finish before the resident database is closed.
	<-shutdownDone
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

// openCodeGoKey is the key DSH uses for its OpenCode Go route; the same key
// reads that subscription's limits.
func openCodeGoKey(cfg config.Config) string {
	return cfg.DSHProviderKeys["opencode-go"]
}

// usageCmd reads the limits once, for checking that the reporter is set up.
func usageCmd() error {
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	cfg := store.Get()
	limits := usage.New(cfg.UsageCommand, time.Duration(cfg.UsageRefreshMinutes)*time.Minute).
		WithOpenCodeGoKey(func() string { return openCodeGoKey(cfg) })
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
		"title":     "Herdr Companion Gateway test",
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

// sidecarPath finds herdr-claude-sidecar: configured, from the environment,
// next to this executable (as the Mac app bundles it), or on PATH.
func sidecarPath(configured string) string {
	const name = "herdr-claude-sidecar"
	if configured != "" {
		return configured
	}
	if p := os.Getenv("HERDR_CLAUDE_SIDECAR"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// claudePath finds the user's Claude Code CLI. A GUI app's PATH is short, so
// the native installer's location is tried too.
func claudePath(configured string) string {
	if configured != "" {
		return configured
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "claude"), filepath.Join(home, ".claude", "local", "claude")} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// residentEnv gives resident tasks the checks new sessions get: workspace
// roots, the agents' own folder trust and their model catalogs.
type residentEnv struct {
	launch    *launcher.Launcher
	providers []providers.Provider
}

func (e residentEnv) ResolveDir(p string) (string, error) { return e.launch.ResolveDir(p) }

func (e residentEnv) FolderTrusted(provider, cwd string) bool {
	for _, p := range e.providers {
		if p.Name() != provider {
			continue
		}
		if tc, ok := p.(providers.TrustChecker); ok {
			return tc.FolderTrusted(cwd)
		}
		return true
	}
	return false
}

func (e residentEnv) Catalog(ctx context.Context, provider string) (providers.ModelCatalog, error) {
	return e.launch.Models(ctx, provider)
}

// agentRuntime is a provider the Gateway runs agents with.
type agentRuntime interface {
	providers.Provider
	providers.DirectRuntime
}

func agentProviders(agents []agentRuntime) []providers.Provider {
	out := make([]providers.Provider, len(agents))
	for i, a := range agents {
		out[i] = a
	}
	return out
}

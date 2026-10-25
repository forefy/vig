// vig is the API server and the developer command set:
//
//	vig                 serve on PORT (default 7112)
//	vig openapi         print the OpenAPI document and exit
//	vig migrate up      apply migrations (also down, status)
//	vig db              run an embedded Postgres on :7132 for developers without Docker
//	vig seed            write the development dataset (idempotent, refuses in prod)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/forefy/vig/backend/internal/adapter/ethereum"
	"github.com/forefy/vig/backend/internal/adapter/repo"
	"github.com/forefy/vig/backend/internal/adapter/solana"
	"github.com/forefy/vig/backend/internal/adapter/webapp"
	"github.com/forefy/vig/backend/internal/api"
	"github.com/forefy/vig/backend/internal/auth"
	"github.com/forefy/vig/backend/internal/chain"
	"github.com/forefy/vig/backend/internal/config"
	"github.com/forefy/vig/backend/internal/core"
	"github.com/forefy/vig/backend/internal/db"
	"github.com/forefy/vig/backend/internal/db/testdb"
	"github.com/forefy/vig/backend/internal/evm"
	"github.com/forefy/vig/backend/internal/github"
	"github.com/forefy/vig/backend/internal/jobs"
	"github.com/forefy/vig/backend/internal/notify"
	"github.com/forefy/vig/backend/internal/scan"
	"github.com/forefy/vig/backend/internal/seed"
	"github.com/forefy/vig/backend/internal/store"
)

const usage = `usage: vig [openapi | migrate up|down|status | db | seed | evm-spike <address> [blocks]]`

func main() {
	setupLogging(env("LOG_FORMAT", "text"))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "openapi":
			if err := api.WriteOpenAPI(os.Stdout, map[string]core.Adapter{solana.Key: solana.New(), ethereum.Key: ethereum.New(), webapp.Key: webapp.New(), repo.Key: repo.New()}); err != nil {
				fatal(err)
			}
		case "migrate":
			dir := "up"
			if len(os.Args) > 2 {
				dir = os.Args[2]
			}
			if err := db.Migrate(context.Background(), mustEnv("DATABASE_URL"), dir); err != nil {
				fatal(err)
			}
		case "db":
			runDB()
		case "seed":
			runSeed()
		case "evm-spike":
			runEVMSpike(os.Args[2:])
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		return
	}
	serve()
}

func serve() {
	cfg, err := config.FromEnv()
	if err != nil {
		fatal(err)
	}
	setupLogging(cfg.LogFormat)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer st.Close()

	d := deps(cfg, st)
	d.BaseCtx = ctx
	if cfg.Env == "dev" {
		slog.Info("dev login enabled", "url", cfg.BackendURL+"/v1/auth/dev/login?user=<name>")
	}
	if os.Getenv("VIG_WORKERS") != "0" {
		go (&jobs.Housekeeping{Store: st, GitHub: d.GitHub, Notifier: d.Notifier}).Loop(ctx)
		go d.Scans.Loop(ctx)
		go (&notify.Dispatcher{Notifier: d.Notifier}).Loop(ctx)
		go (&jobs.PurchaseConfirmer{Store: st, Chain: d.Chain, Notifier: d.Notifier, Treasury: cfg.TreasuryPubkey, USDCMint: cfg.USDCMint}).Loop(ctx)
		builder := &solana.SurfaceBuilder{Chain: d.Chain, Osec: d.Inspector.Osec}
		eth := d.Adapters[ethereum.Key].(*ethereum.Adapter)
		inspectors := map[string]core.ProgramInspector{}
		for key, a := range d.Adapters {
			if insp, ok := a.(core.ProgramInspector); ok {
				inspectors[key] = insp
			}
		}
		go (&jobs.SurfaceMapper{Store: st, GitHub: d.GitHub, Builders: map[string]core.SurfaceBuilder{solana.Key: builder, ethereum.Key: eth}, Inspectors: inspectors}).Loop(ctx)
		go (&jobs.Watch{Store: st, Notifier: d.Notifier, Watchers: map[string]core.Watcher{solana.Key: &solana.Watcher{SurfaceBuilder: builder, URLs: cfg.RPCURLs}, ethereum.Key: eth}}).Loop(ctx)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.NewRouter(d),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	go func() {
		slog.Info("listening", "version", api.Version, "commit", api.Commit, "addr", ":"+cfg.Port, "docs", "/docs", "static", cfg.StaticDir != "", "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

// deps wires every integration from the configuration. Missing credentials
// leave the matching dependency in a state that answers 503.
func deps(cfg config.Config, st *store.Store) *api.Deps {
	rpc := chain.New(cfg.RPCURLs)
	inspector := &solana.Inspector{Chain: rpc, Osec: chain.NewOsec(cfg.OsecURL)}
	gh := github.New(cfg.GitHubAPI, cfg.GitHubToken, cfg.DataDir)
	app, err := github.NewApp(cfg.GitHubAPI, cfg.GitHubAppID, cfg.GitHubAppPrivateKey, cfg.GitHubAppWebhookSecret)
	if err != nil {
		fatal(err)
	}
	adapter := solana.NewWired(inspector, scan.NewDB(filepath.Join(cfg.DataDir, "advisories")))
	adapters := map[string]core.Adapter{solana.Key: adapter, ethereum.Key: ethereum.NewWired(cfg.EVMRPC), webapp.Key: webapp.NewWired(), repo.Key: repo.New()}
	// The operator's match result rewrites the adapter's panel rows so the
	// badges and the columns never disagree.
	st.PanelPatch = solana.MatchRows
	discord := notify.NewDiscord(cfg.DiscordBotToken)
	email := notify.NewEmail(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
	return &api.Deps{
		Config:       cfg,
		Store:        st,
		Signer:       auth.NewSigner(cfg.SessionSecret, cfg.IsProd()),
		Providers:    auth.Providers(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.DiscordClientID, cfg.DiscordClientSecret),
		Chain:        rpc,
		Inspector:    inspector,
		GitHub:       gh,
		App:          app,
		Adapters:     adapters,
		AdapterOrder: []string{solana.Key, ethereum.Key, webapp.Key},
		Notifier:     notify.New(st, discord, email),
		Discord:      discord,
		Scans:        &jobs.ScanRunner{Store: st, GitHub: gh, Adapters: adapters},
		HTTP:         &http.Client{Timeout: 30 * time.Second},
		Now:          time.Now,
	}
}

// runDB runs an embedded Postgres until interrupted. Data lives under
// DATA_DIR/pg so it survives restarts.
func runDB() {
	dataDir := env("DATA_DIR", "./data")
	port := env("VIG_DB_PORT", "7132")
	var p uint32
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		fatal(fmt.Errorf("VIG_DB_PORT: %w", err))
	}
	pgDir := filepath.Join(dataDir, "pg")
	if err := os.MkdirAll(pgDir, 0o755); err != nil {
		fatal(err)
	}
	pg := embeddedpostgres.NewDatabase(testdb.Config(p, pgDir).Logger(os.Stdout))
	if err := pg.Start(); err != nil {
		fatal(err)
	}
	slog.Info("postgres listening", "addr", "127.0.0.1:"+port, "data", pgDir)
	fmt.Printf("DATABASE_URL=postgres://vig:vig@127.0.0.1:%s/vig?sslmode=disable\n", port)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	if err := pg.Stop(); err != nil {
		fatal(err)
	}
}

func runSeed() {
	cfg, err := config.FromEnv()
	if err != nil {
		fatal(err)
	}
	if cfg.IsProd() {
		slog.Error("seed refuses to run with VIG_ENV=prod")
		os.Exit(1)
	}
	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer st.Close()
	res, err := (&seed.Seeder{Store: st, Signer: auth.NewSigner(cfg.SessionSecret, false), Secret: cfg.SessionSecret, Env: cfg.Env, GitHub: github.New(cfg.GitHubAPI, cfg.GitHubToken, cfg.DataDir)}).Run(ctx)
	if err != nil {
		fatal(err)
	}
	fmt.Print(res.Summary())
	fmt.Printf("  dev login        %s/v1/auth/dev/login?user=admin (also pro, free)\n", cfg.BackendURL)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fatal(fmt.Errorf("%s is required", k))
	}
	return v
}

// setupLogging installs the process logger: JSON when format is json (the
// prod default), key=value text otherwise. Everything, chi requests included,
// goes through slog.Default.
func setupLogging(format string) {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// fatal writes err at error level and exits 1.
func fatal(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}

// runEVMSpike samples one Ethereum contract from the public endpoint and
// prints what a surface map would know of it without traces: the
// question the Ethereum adapter decision rests on.
func runEVMSpike(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: vig evm-spike <address> [blocks]")
		os.Exit(2)
	}
	blocks := 10
	if len(args) > 1 {
		if n, err := strconv.Atoi(args[1]); err == nil && n > 0 && n <= 200 {
			blocks = n
		}
	}
	var urls []string
	if u := os.Getenv("EVM_RPC_URL"); u != "" {
		urls = []string{u}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	r, err := evm.Sample(ctx, evm.New(urls), evm.NewLookup(), args[0], evm.Budget{Blocks: blocks, Receipts: 40, Callers: 8, Pace: 150 * time.Millisecond})
	if err != nil {
		fatal(err)
	}
	fmt.Print(r.Text())
}

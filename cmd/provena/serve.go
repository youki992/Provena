//go:build webconsole

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chobits02/provena/internal/app"
	"github.com/chobits02/provena/internal/config"
	"github.com/chobits02/provena/internal/database"
	"github.com/chobits02/provena/internal/logger"
	"github.com/chobits02/provena/internal/security"
	"github.com/chobits02/provena/internal/termout"

	"go.uber.org/zap"
	"golang.org/x/term"
)

// serveCompiled tells the usage text whether this binary can start the web
// console. The console is opt-in: a plain `go build` gets serve_stub.go instead.
const serveCompiled = true

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "Path to the configuration file")
	httpsBootstrap := fs.Bool("https", false, "Enable HTTPS for the main site; uses an in-memory self-signed certificate when no cert/key is configured")
	httpBootstrap := fs.Bool("http", false, "Force plain HTTP for the main site, overriding TLS settings in the configuration file")
	resetAdminPassword := fs.Bool("reset-admin-password", false, "Interactively reset the built-in admin password and exit")
	_ = fs.Parse(args)

	if *httpsBootstrap && *httpBootstrap {
		return fmt.Errorf("--http and --https cannot be used together")
	}
	// Environment variable compatibility, so systemd/docker can configure TLS
	// without passing flags.
	if !*httpsBootstrap && !*httpBootstrap {
		if v := strings.TrimSpace(os.Getenv("PROVENA_HTTPS")); v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
			*httpsBootstrap = true
		}
	}

	cp := strings.TrimSpace(*configPath)
	if cp == "" {
		cp = defaultConfigPath
	}
	if strings.HasPrefix(cp, "-") {
		return fmt.Errorf("invalid -config path %q; -config must be followed by a yaml file path", cp)
	}

	localConfig, err := config.EnsureLocalConfig(cp)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	cfg, err := config.Load(cp)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if localConfig.Created {
		termout.PrintConfigCreated()
	}

	if *resetAdminPassword {
		return runResetAdminPassword(cfg)
	}

	if *httpBootstrap {
		config.ApplyPlainHTTPBootstrap(cfg)
	} else if *httpsBootstrap {
		config.ApplyDevHTTPSBootstrap(cfg)
	}

	// When MCP is enabled without an auth header value, generate a random key
	// and persist it back to the config file.
	if err := config.EnsureMCPAuth(cp, cfg); err != nil {
		return fmt.Errorf("failed to configure MCP authentication: %w", err)
	}
	if cfg.MCP.Enabled {
		config.PrintMCPConfigJSON(cfg.MCP)
	}

	log := logger.New(cfg.Log.Level, cfg.Log.Output)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	application, err := app.New(cfg, log, cp)
	if err != nil {
		return fmt.Errorf("application init failed: %w", err)
	}

	go func() {
		sig := <-sigCh
		log.Info("received system signal, shutting down gracefully: " + sig.String())
		application.Shutdown()
		cancel()
	}()

	// Only print ONLINE once the listener has actually bound, so a port conflict
	// is not reported as a successful start.
	err = application.RunWithContextReady(ctx, func() {
		port := cfg.Server.Port
		if port <= 0 {
			port = 8080
		}
		scheme := "http"
		if config.MainWebUIUsesHTTPS(&cfg.Server) {
			scheme = "https"
		}
		termout.PrintStartupWebUI(termout.StartupWebUIOptions{
			Scheme:       scheme,
			Port:         port,
			SelfSigned:   scheme == "https" && cfg.Server.TLSAutoSelfSign,
			HTTPRedirect: scheme == "https" && config.ServerHTTPRedirectEnabled(&cfg.Server),
		})
	})
	if err != nil {
		// A cancelled context is a graceful shutdown, not a failure.
		if ctx.Err() != nil {
			log.Info("server shut down gracefully")
			return nil
		}
		return fmt.Errorf("server failed to start: %w", err)
	}
	return nil
}

func runResetAdminPassword(cfg *config.Config) error {
	dbPath := strings.TrimSpace(cfg.Database.Path)
	if dbPath == "" {
		dbPath = "data/conversations.db"
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("database does not exist: %s; start the service once to initialize it first", dbPath)
		}
		return err
	}

	fmt.Println("Reset built-in admin password")
	fmt.Println()

	password, err := readHiddenPassword("New admin password: ")
	if err != nil {
		return err
	}
	password = strings.TrimSpace(password)
	if len(password) < 8 {
		return fmt.Errorf("new password must be at least 8 characters")
	}
	confirm, err := readHiddenPassword("Confirm new password: ")
	if err != nil {
		return err
	}
	if password != strings.TrimSpace(confirm) {
		return fmt.Errorf("passwords do not match")
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}

	db, err := database.NewDB(dbPath, zap.NewNop())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	admin, err := db.GetRBACUserByUsername("admin")
	if err != nil {
		return fmt.Errorf("built-in admin account was not found; start the service once to initialize it first: %w", err)
	}
	if !admin.IsBuiltin {
		return fmt.Errorf("admin account is not built in; refusing to reset it")
	}
	if err := db.UpdateRBACAdminPassword(hash); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Admin password has been reset.")
	fmt.Println("If the service is running, existing login sessions remain valid until the service restarts or the sessions expire.")
	return nil
}

func readHiddenPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(password), nil
}

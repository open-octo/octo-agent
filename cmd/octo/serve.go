package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Leihb/octo-agent/internal/server"
)

// runServe handles `octo serve`.
func runServe(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8080", "Bind address (e.g. :8080, 127.0.0.1:8080)")
	provider := fs.String("provider", "", "Provider: anthropic | openai")
	model := fs.String("model", "", "Model name")
	system := fs.String("system", "", "System prompt")
	maxTokens := fs.Int("max-tokens", 0, "max_tokens for responses")
	tools := fs.Bool("tools", true, "Enable agentic tool loop")
	cors := fs.String("cors", "", "CORS allowed origins (comma-separated, * for any)")
	noChannel := fs.Bool("no-channel", false, "Disable IM channel (DingTalk, Feishu)")
	noMemory := fs.Bool("no-memory", false, "Disable cross-session memory injection")
	noSupervisor := fs.Bool("no-supervisor", false, "Run the server directly, without the self-restart supervisor (exit code 42 still signals a restart request to an external supervisor)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if shouldSupervise(*noSupervisor, os.Getenv(serveWorkerEnv)) {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		return superviseLoop(spawnServeWorker(args, stdout, stderr), sigCh, stderr)
	}

	var corsOrigins []string
	if *cors != "" {
		for _, o := range splitComma(*cors) {
			o = strings.TrimSpace(o)
			if o != "" {
				corsOrigins = append(corsOrigins, o)
			}
		}
	}

	cfg := server.Config{
		Addr:        *addr,
		Provider:    *provider,
		Model:       *model,
		System:      *system,
		MaxTokens:   *maxTokens,
		Tools:       *tools,
		CORSOrigins: corsOrigins,
		NoChannel:   *noChannel,
		NoMemory:    *noMemory,
	}

	srv, err := server.New(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "octo serve: %v\n", err)
		return 1
	}

	host := *addr
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	fmt.Fprintf(stdout, "octo server listening on http://%s\n", host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		fmt.Fprintln(stdout, "\nocto serve: shutting down...")
		_ = srv.Shutdown(ctx)
	}()

	err = srv.ListenAndServe()
	switch {
	case err == nil:
	case errors.Is(err, server.ErrRestartRequested):
		fmt.Fprintln(stdout, "octo serve: restarting...")
	default:
		fmt.Fprintf(stderr, "octo serve: %v\n", err)
	}
	return serveExitCode(err)
}

// shouldSupervise decides whether `octo serve` runs as the supervisor parent
// (the default) or as the worker. The worker marker env covers both the
// supervisor's own child and users running under an external supervisor;
// --no-supervisor is the explicit flag form of the same opt-out.
func shouldSupervise(noSupervisor bool, workerEnv string) bool {
	return !noSupervisor && workerEnv != "1"
}

// serveExitCode maps the worker's ListenAndServe result onto the process
// exit-code contract: clean stop → 0, restart request → ExitRestart,
// anything else → 1.
func serveExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, server.ErrRestartRequested):
		return server.ExitRestart
	default:
		return 1
	}
}

func splitComma(s string) []string {
	return strings.Split(s, ",")
}

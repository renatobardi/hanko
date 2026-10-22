// Comando hanko-evals: evals de comportamento e chaos (#58). Sem ANTHROPIC_API_KEY e sem HANKO_EVALS_LIVE=1
// roda só contra o roteiro de mentira, sem custo. Ver docs/validacao/trilha-w2b.md.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/renatobardi/hanko/internal/evals"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := evals.Main(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

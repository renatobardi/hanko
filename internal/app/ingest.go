package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/service"
)

// partitionEvery é de quanto em quanto tempo o job confere as partições mensais da telemetria (Q46: todo dia).
const partitionEvery = 24 * time.Hour

// startIngest abre o listener da ingestão (HANKO_INGEST_ADDR, ADR-0076 item 1) e o job das partições mensais. Sem o
// endereço, a ingestão fica desligada e nada abre. O listener é aberto antes de a função voltar: endereço ocupado é erro
// de partida, não uma ingestão silenciosamente fora do ar. A função devolvida espera o encerramento (ctx cancelado).
func startIngest(ctx context.Context, cfg config.Config, svc *service.Service, logger *slog.Logger) (wait func(), err error) {
	if cfg.IngestAddr == "" {
		logger.Info("ingestão desligada (sem HANKO_INGEST_ADDR)")
		return func() {}, nil
	}
	ln, err := net.Listen("tcp", cfg.IngestAddr)
	if err != nil {
		return nil, fmt.Errorf("app: escutar a ingestão em %s: %w", cfg.IngestAddr, err)
	}
	h := ingest.NewHandler(ingest.Deps{Resolver: svc, Writer: svc, Ready: svc.IngestReady, Now: svc.Now, Log: logger})
	srv := newHTTPServer(cfg.IngestAddr, h, logger)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("ingestão: servidor parou", "err", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownGrace)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			_ = srv.Close()
		}
	}()
	go partitionLoop(ctx, svc, logger)
	logger.Info("ingestão no ar", "addr", ln.Addr().String())
	return wg.Wait, nil
}

// partitionLoop cria as partições mensais na partida e depois todo dia. Falha só registra: a ingestão segue gravando na
// partição `DEFAULT` e a prontidão da ingestão avisa quando faltar a do mês corrente.
func partitionLoop(ctx context.Context, svc *service.Service, logger *slog.Logger) {
	ensure := func() {
		if err := svc.EnsureTelemetryPartitions(ctx, svc.Now()); err != nil && ctx.Err() == nil {
			logger.Error("ingestão: criar partições mensais falhou", "err", err)
		}
	}
	ensure()
	t := time.NewTicker(partitionEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ensure()
		}
	}
}

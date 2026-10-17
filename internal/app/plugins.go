package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/service"
)

// setupPlugins liga os plugins de terceiros quando o operador definiu HANKO_PLUGINS_DIR (ADR-0038). start sobe
// os processos que devem estar de pé (depois da fila); stop os encerra no desligamento do servidor.
func setupPlugins(cfg config.Config, svc *service.Service, logger *slog.Logger, version string) (start func(context.Context), stop func()) {
	if cfg.Plugins.Dir == "" {
		logger.Info("plugins de terceiros desligados (HANKO_PLUGINS_DIR vazio)")
		return func(context.Context) {}, func() {}
	}
	mgr := plugin.NewManager(plugin.Options{HostVersion: version, Logger: logger,
		Handler: svc.HandlePluginCall, OnLog: svc.PluginLogLine, OnState: svc.PluginProcessState})
	svc.Plugins = service.PluginsConfig{Dir: cfg.Plugins.Dir, DataDir: cfg.Plugins.DataDir, Runtime: mgr}
	logger.Warn("plugins de terceiros ligados (HANKO_PLUGINS_DIR); cada plugin instalado roda como processo nesta máquina",
		"dir", cfg.Plugins.Dir, "data_dir", cfg.Plugins.DataDir)
	return func(ctx context.Context) { svc.ReconcilePlugins(ctx) }, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		mgr.StopAll(ctx)
	}
}

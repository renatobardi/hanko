//go:build tools

// Package deps pins the v0.1 dependency set in go.mod so parallel work never edits it.
package deps

import (
	_ "github.com/a-h/templ"
	_ "github.com/caarlos0/env/v11"
	_ "github.com/danielgtaylor/huma/v2"
	_ "github.com/danielgtaylor/huma/v2/adapters/humago"
	_ "github.com/exaring/otelpgx"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/jackc/pgx/v5"
	_ "github.com/modelcontextprotocol/go-sdk/mcp"
	_ "github.com/pressly/goose/v3"
	_ "github.com/prometheus/client_golang/prometheus/promhttp"
	_ "github.com/riverqueue/river"
	_ "github.com/riverqueue/river/riverdriver/riverpgxv5"
	_ "github.com/riverqueue/river/rivermigrate"
	_ "github.com/riverqueue/rivercontrib/otelriver"
	_ "github.com/robfig/cron/v3"
	_ "github.com/spf13/cobra"
	_ "github.com/yuin/goldmark"
	_ "go.opentelemetry.io/contrib/bridges/otelslog"
	_ "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	_ "go.opentelemetry.io/otel"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	_ "go.opentelemetry.io/otel/exporters/prometheus"
	_ "go.opentelemetry.io/otel/sdk/log"
	_ "go.opentelemetry.io/otel/sdk/metric"
	_ "go.opentelemetry.io/otel/sdk/trace"
	_ "golang.org/x/crypto/argon2"
	_ "gopkg.in/yaml.v3"
)

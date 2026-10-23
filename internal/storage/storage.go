// Package storage guarda o conteúdo dos artefatos atrás da interface BlobStore (ADR-0051). Três drivers: postgres
// (o padrão, a tabela artifact_blobs, dentro da transação do artefato), s3 (qualquer endpoint S3 compatível, com
// SigV4 próprio sobre a stdlib) e fs (arquivos num diretório local, ADR-0065). O artefato registra em que driver está
// (artifacts.storage), então os drivers convivem.
package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/domain"
)

// Nomes dos drivers (os mesmos valores de artifacts.storage).
const (
	DriverPostgres = "postgres"
	DriverS3       = "s3"
	DriverFS       = "fs"
)

// ErrNotFound: o driver não tem conteúdo para a chave.
var ErrNotFound = errors.New("storage: conteúdo não encontrado")

// Key identifica o conteúdo de uma versão de artefato.
type Key struct {
	CompanyID  domain.ID
	ArtifactID domain.ID
}

func (k Key) String() string { return k.CompanyID.String() + "/" + k.ArtifactID.String() }

// BlobStore guarda e devolve o conteúdo de uma versão de artefato.
type BlobStore interface {
	// Name é o nome do driver ("postgres" | "s3" | "fs").
	Name() string
	// Transactional informa que Put deve rodar dentro da transação do artefato (a transação vai no contexto,
	// WithTx). Falso: o conteúdo é gravado antes da transação, com compensação em caso de falha.
	Transactional() bool
	Put(ctx context.Context, k Key, data []byte, contentType string) error
	// Get devolve o conteúdo; ErrNotFound se não existe.
	Get(ctx context.Context, k Key) ([]byte, error)
	// Delete é idempotente: apagar o que não existe não é erro.
	Delete(ctx context.Context, k Key) error
	// Stat devolve o tamanho guardado (a migração confere a cópia com ele); ErrNotFound se não existe.
	Stat(ctx context.Context, k Key) (int64, error)
}

// Registry reúne os drivers configurados e diz qual recebe as escritas novas.
type Registry struct {
	active  string
	drivers map[string]BlobStore
}

// NewRegistry monta o registro com o driver ativo e os demais disponíveis para leitura/migração. Cada driver é
// instrumentado (span e métricas).
func NewRegistry(active BlobStore, others ...BlobStore) *Registry {
	r := &Registry{active: active.Name(), drivers: map[string]BlobStore{}}
	for _, d := range append([]BlobStore{active}, others...) {
		if _, dup := r.drivers[d.Name()]; !dup {
			r.drivers[d.Name()] = instrument(d)
		}
	}
	return r
}

// Active é o driver das escritas novas.
func (r *Registry) Active() BlobStore { return r.drivers[r.active] }

// Driver devolve o driver pelo nome.
func (r *Registry) Driver(name string) (BlobStore, error) {
	if r == nil {
		return nil, fmt.Errorf("storage: driver %q não configurado", name)
	}
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("storage: driver %q não está configurado neste servidor", name)
	}
	return d, nil
}

// Names lista os drivers disponíveis, em ordem.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.drivers))
	for n := range r.drivers {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// instrumented acrescenta span e métricas a qualquer driver.
type instrumented struct {
	BlobStore
	tracer trace.Tracer
	ops    metric.Int64Counter
	bytes  metric.Int64Counter
}

var (
	metricsOnce sync.Once
	opsCounter  metric.Int64Counter
	byteCounter metric.Int64Counter
)

func instrument(b BlobStore) BlobStore {
	metricsOnce.Do(func() {
		m := otel.Meter("hanko/storage")
		opsCounter, _ = m.Int64Counter("hanko.blob.ops", metric.WithDescription("operações no BlobStore, por driver, operação e resultado"))
		byteCounter, _ = m.Int64Counter("hanko.blob.bytes", metric.WithUnit("By"), metric.WithDescription("bytes movidos pelo BlobStore"))
	})
	return &instrumented{BlobStore: b, tracer: otel.Tracer("hanko/storage"), ops: opsCounter, bytes: byteCounter}
}

func (i *instrumented) done(ctx context.Context, span trace.Span, op string, n int64, err error) {
	result := "ok"
	switch {
	case errors.Is(err, ErrNotFound):
		result = "not_found"
	case err != nil:
		result = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "blob "+op)
	}
	attrs := metric.WithAttributes(attribute.String("driver", i.Name()), attribute.String("op", op), attribute.String("result", result))
	if i.ops != nil {
		i.ops.Add(ctx, 1, attrs)
	}
	if n > 0 && err == nil && i.bytes != nil {
		i.bytes.Add(ctx, n, attrs)
	}
	span.SetAttributes(attribute.Int64("hanko.blob.size", n))
	span.End()
}

func (i *instrumented) start(ctx context.Context, op string, k Key) (context.Context, trace.Span) {
	return i.tracer.Start(ctx, "hanko.blob."+op, trace.WithAttributes(
		attribute.String("hanko.blob.driver", i.Name()), attribute.String("hanko.company_id", k.CompanyID.String()),
		attribute.String("hanko.artifact_id", k.ArtifactID.String())))
}

func (i *instrumented) Put(ctx context.Context, k Key, data []byte, ct string) error {
	ctx, span := i.start(ctx, "put", k)
	err := i.BlobStore.Put(ctx, k, data, ct)
	i.done(ctx, span, "put", int64(len(data)), err)
	return err
}

func (i *instrumented) Get(ctx context.Context, k Key) ([]byte, error) {
	ctx, span := i.start(ctx, "get", k)
	b, err := i.BlobStore.Get(ctx, k)
	i.done(ctx, span, "get", int64(len(b)), err)
	return b, err
}

func (i *instrumented) Delete(ctx context.Context, k Key) error {
	ctx, span := i.start(ctx, "delete", k)
	err := i.BlobStore.Delete(ctx, k)
	i.done(ctx, span, "delete", 0, err)
	return err
}

func (i *instrumented) Stat(ctx context.Context, k Key) (int64, error) {
	ctx, span := i.start(ctx, "stat", k)
	n, err := i.BlobStore.Stat(ctx, k)
	i.done(ctx, span, "stat", 0, err)
	return n, err
}

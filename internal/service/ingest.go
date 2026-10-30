package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/store"
)

// Ingestão de telemetria de agente externo (ADR-0076): a chave `ingest`, a gravação do lote e as partições mensais.

func ingestKeyFromRow(r store.ApiKey) domain.IngestKey {
	k := domain.IngestKey{ID: r.ID, Name: r.Name, Prefix: r.Prefix, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt}
	if r.CompanyID != nil {
		k.CompanyID = *r.CompanyID
	}
	return k
}

// CreateIngestKey cria uma chave de ingestão da empresa (owner ou admin). O token em claro só volta aqui. A chave
// pertence ao usuário que a criou (`api_keys.user_id`), mas só vale na ingestão: nenhuma rota pública a aceita.
func (s *Service) CreateIngestKey(ctx context.Context, actor domain.Actor, companyID domain.ID, name string) (string, domain.IngestKey, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return "", domain.IngestKey{}, err
	}
	if actor.Type != domain.ActorHuman || actor.ID == nil {
		return "", domain.IngestKey{}, domain.ErrForbidden
	}
	name = strings.TrimSpace(name)
	if err := domain.ValidateExternalName(name); err != nil {
		return "", domain.IngestKey{}, err
	}
	token, prefix, hash, err := auth.NewAPIKey()
	if err != nil {
		return "", domain.IngestKey{}, err
	}
	var out domain.IngestKey
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateIngestKey(ctx, store.CreateIngestKeyParams{ID: domain.NewID(), UserID: *actor.ID, Name: name,
			Prefix: prefix, Hash: hash, CompanyID: &companyID, Now: t.now})
		if err != nil {
			return mapErr(err, "create ingest key")
		}
		out = ingestKeyFromRow(row)
		return t.activity(actor, companyID, "ingest_key.created", "ingest_key", out.ID, map[string]any{"name": out.Name, "prefix": out.Prefix}, "")
	})
	if err != nil {
		return "", domain.IngestKey{}, err
	}
	return token, out, nil
}

// ListIngestKeys lista as chaves de ingestão da empresa (owner ou admin).
func (s *Service) ListIngestKeys(ctx context.Context, companyID domain.ID, includeRevoked bool, limit, offset int) ([]domain.IngestKey, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	rows, err := s.read().ListIngestKeys(ctx, store.ListIngestKeysParams{CompanyID: &companyID, IncludeRevoked: includeRevoked,
		PageLimit: i32(limit), PageOffset: i32(offset)})
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	out := make([]domain.IngestKey, len(rows))
	for i, r := range rows {
		out[i] = ingestKeyFromRow(r)
	}
	return out, nil
}

// RevokeIngestKey revoga uma chave de ingestão da empresa (owner ou admin). Chave de outra empresa, inexistente ou já
// revogada = ErrNotFound.
func (s *Service) RevokeIngestKey(ctx context.Context, actor domain.Actor, companyID, keyID domain.ID) (domain.IngestKey, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.IngestKey{}, err
	}
	var out domain.IngestKey
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.RevokeIngestKey(ctx, store.RevokeIngestKeyParams{ID: keyID, CompanyID: &companyID, Now: t.now})
		if err != nil {
			return mapErr(err, "ingest key")
		}
		out = ingestKeyFromRow(row)
		return t.activity(actor, companyID, "ingest_key.revoked", "ingest_key", out.ID, map[string]any{"name": out.Name, "prefix": out.Prefix}, "")
	})
	return out, err
}

// ResolveIngestKey acha a chave de ingestão de um token (ingest.Resolver). Uma chave válida de outro escopo é
// ErrWrongScope; chave ausente, revogada ou sem empresa é ErrUnauthenticated. O último uso é anotado no máximo 1×/min.
func (s *Service) ResolveIngestKey(ctx context.Context, token string) (ingest.Key, error) {
	if !auth.IsAPIKey(token) {
		return ingest.Key{}, ingest.ErrUnauthenticated
	}
	q := s.read()
	row, err := q.GetAPIKeyByHash(ctx, auth.HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return ingest.Key{}, ingest.ErrUnauthenticated
		}
		return ingest.Key{}, fmt.Errorf("get ingest key: %w", err)
	}
	if domain.KeyScope(row.Scope) != domain.KeyScopeIngest {
		return ingest.Key{}, ingest.ErrWrongScope
	}
	if row.CompanyID == nil {
		return ingest.Key{}, ingest.ErrUnauthenticated
	}
	now := s.Now().UTC()
	if row.LastUsedAt == nil || now.Sub(*row.LastUsedAt) >= auth.TouchEvery {
		if err := q.TouchAPIKey(ctx, store.TouchAPIKeyParams{ID: row.ID, Now: now}); err != nil && !errors.Is(err, context.Canceled) {
			return ingest.Key{}, fmt.Errorf("touch ingest key: %w", err)
		}
	}
	return ingest.Key{ID: row.ID, CompanyID: *row.CompanyID}, nil
}

// WriteTelemetry grava um lote de telemetria bruta numa transação só (ingest.Writer). Cada tabela recebe o lote por COPY
// numa tabela temporária e depois por INSERT ... ON CONFLICT DO NOTHING: um fato repetido não duplica nada, nem dentro do
// mesmo lote (pesquisa da #68, §4). O fato sem hora (hora = chegada) tem a chave conferida em todas as partições. O handler responde 2xx só depois do commit.
func (s *Service) WriteTelemetry(ctx context.Context, key ingest.Key, b ingest.Batch) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("ingest: iniciar transação: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := copyTelemetry(ctx, tx, "telemetry_spans", ingest.SpanColumns, b.Spans, func(r ingest.SpanRow) []any { return r.Values(key) }); err != nil {
		return err
	}
	if err := copyTelemetry(ctx, tx, "telemetry_logs", ingest.LogColumns, b.Logs, func(r ingest.LogRow) []any { return r.Values(key) }); err != nil {
		return err
	}
	if err := copyTelemetry(ctx, tx, "telemetry_metrics", ingest.MetricColumns, b.Metrics, func(r ingest.MetricRow) []any { return r.Values(key) }); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ingest: commit: %w", err)
	}
	return nil
}

func copyTelemetry[T any](ctx context.Context, tx pgx.Tx, table string, cols []string, rows []T, values func(T) []any) error {
	if len(rows) == 0 {
		return nil
	}
	stage := "stage_" + table
	if _, err := tx.Exec(ctx, fmt.Sprintf("CREATE TEMP TABLE %s (LIKE %s INCLUDING DEFAULTS) ON COMMIT DROP", stage, table)); err != nil {
		return fmt.Errorf("ingest: tabela temporária de %s: %w", table, err)
	}
	data := make([][]any, len(rows))
	for i, r := range rows {
		data[i] = values(r)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{stage}, cols, pgx.CopyFromRows(data)); err != nil {
		return fmt.Errorf("ingest: copiar para %s: %w", table, err)
	}
	list := strings.Join(cols, ", ")
	// A chave primária leva a hora do fato (exigência do particionamento), então só ela não pega o fato sem hora, cuja
	// hora vira a da chegada e muda a cada reenvio. Para esses (hora == chegada) confere-se a chave em todas as partições.
	q := fmt.Sprintf(`INSERT INTO %[1]s (%[2]s) SELECT %[2]s FROM %[3]s s
		WHERE s.time_unix_nano <> s.received_unix_nano
		   OR NOT EXISTS (SELECT 1 FROM %[1]s x WHERE x.company_id = s.company_id AND x.dedupe_key = s.dedupe_key)
		ON CONFLICT DO NOTHING`, table, list, stage)
	if _, err := tx.Exec(ctx, q); err != nil {
		return fmt.Errorf("ingest: gravar em %s: %w", table, err)
	}
	return nil
}

// ---------- partições mensais ----------

var telemetryTables = []string{"telemetry_spans", "telemetry_logs", "telemetry_metrics"}

// PartitionMonthsAhead é quantos meses à frente o job mantém criados (Q46: dois meses de antecedência).
const PartitionMonthsAhead = 2

func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// EnsureTelemetryPartitions cria, em cada tabela de telemetria, a partição do mês de `now` e as dos PartitionMonthsAhead
// meses seguintes, se faltarem. Se a partição `DEFAULT` já tiver linhas da faixa, o Postgres recusa a criação: o erro
// sobe para o job registrar e o `hanko doctor` avisar (a ingestão continua gravando na `DEFAULT`).
func (s *Service) EnsureTelemetryPartitions(ctx context.Context, now time.Time) error {
	var errs []error
	for _, table := range telemetryTables {
		for i := 0; i <= PartitionMonthsAhead; i++ {
			from := monthStart(now).AddDate(0, i, 0)
			to := from.AddDate(0, 1, 0)
			name := fmt.Sprintf("%s_%04d_%02d", table, from.Year(), int(from.Month()))
			q := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM (%d) TO (%d)", name, table,
				from.UnixNano(), to.UnixNano())
			if _, err := s.Pool.Exec(ctx, q); err != nil {
				errs = append(errs, fmt.Errorf("partição %s: %w", name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// IngestReady diz se a ingestão pode receber: o banco responde e as tabelas de telemetria têm a partição do mês corrente.
func (s *Service) IngestReady(ctx context.Context) error {
	if err := s.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("banco: %w", err)
	}
	month := monthStart(s.Now()).Format("2006_01")
	for _, table := range telemetryTables {
		var ok bool
		if err := s.Pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table+"_"+month).Scan(&ok); err != nil {
			return fmt.Errorf("partição de %s: %w", table, err)
		}
		if !ok {
			return fmt.Errorf("falta a partição de %s para %s", table, month)
		}
	}
	return nil
}

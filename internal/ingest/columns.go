package ingest

// As colunas e os valores de cada linha, na ordem em que o gravador as copia para o Postgres. A empresa e a chave que
// entregou o fato são do gravador (vêm da chave de ingestão), nunca do corpo.

func (f Fixed) values() []any {
	return []any{f.HostName, f.OuteInstance, f.OuteAgent, f.ServiceName, f.SessionID, f.OuteTaskID, f.OuteSwarmRound,
		f.OuteRepo, f.OuteSubscription}
}

func concat(parts ...[]any) []any {
	var out []any
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// SpanColumns são as colunas de `telemetry_spans` que o gravador preenche.
var SpanColumns = []string{"company_id", "dedupe_key", "time_unix_nano", "end_unix_nano", "duration_ns", "host_name",
	"oute_instance", "oute_agent", "service_name", "session_id", "oute_task_id", "oute_swarm_round", "oute_repo",
	"oute_subscription", "trace_id", "span_id", "parent_span_id", "name", "kind", "status_code", "status_message", "model",
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens", "cost_usd", "scope_name",
	"resource_attributes", "attributes", "events", "links", "received_unix_nano", "ingested_by_key_id"}

// Values devolve os valores na ordem de SpanColumns.
func (r SpanRow) Values(k Key) []any {
	return concat(
		[]any{k.CompanyID, r.DedupeKey, r.TimeUnixNano, r.EndUnixNano, r.DurationNs},
		r.values(),
		[]any{r.TraceID, r.SpanID, r.ParentSpanID, r.Name, r.Kind, r.StatusCode, r.StatusMessage, r.Model, r.InputTokens,
			r.OutputTokens, r.CacheReadTokens, r.CacheCreationTokens, r.CostUSD, r.ScopeName, r.ResourceAttributes,
			r.Attributes, r.Events, r.Links, r.ReceivedUnixNano, k.ID})
}

// LogColumns são as colunas de `telemetry_logs` que o gravador preenche.
var LogColumns = []string{"company_id", "dedupe_key", "time_unix_nano", "observed_unix_nano", "host_name", "oute_instance",
	"oute_agent", "service_name", "session_id", "oute_task_id", "oute_swarm_round", "oute_repo", "oute_subscription",
	"event_name", "oute_event_id", "severity_number", "severity_text", "body", "trace_id", "span_id", "scope_name",
	"resource_attributes", "attributes", "received_unix_nano", "ingested_by_key_id"}

// Values devolve os valores na ordem de LogColumns.
func (r LogRow) Values(k Key) []any {
	return concat(
		[]any{k.CompanyID, r.DedupeKey, r.TimeUnixNano, r.ObservedUnixNano},
		r.values(),
		[]any{r.EventName, r.OuteEventID, r.SeverityNumber, r.SeverityText, r.Body, r.TraceID, r.SpanID, r.ScopeName,
			r.ResourceAttributes, r.Attributes, r.ReceivedUnixNano, k.ID})
}

// MetricColumns são as colunas de `telemetry_metrics` que o gravador preenche.
var MetricColumns = []string{"company_id", "dedupe_key", "time_unix_nano", "start_unix_nano", "host_name", "oute_instance",
	"oute_agent", "service_name", "session_id", "oute_task_id", "oute_swarm_round", "oute_repo", "oute_subscription",
	"metric_name", "metric_type", "unit", "value", "count", "is_monotonic", "aggregation_temporality", "scope_name",
	"resource_attributes", "attributes", "point", "received_unix_nano", "ingested_by_key_id"}

// Values devolve os valores na ordem de MetricColumns.
func (r MetricRow) Values(k Key) []any {
	return concat(
		[]any{k.CompanyID, r.DedupeKey, r.TimeUnixNano, r.StartUnixNano},
		r.values(),
		[]any{r.MetricName, r.MetricType, r.Unit, r.Value, r.Count, r.IsMonotonic, r.AggregationTemporality, r.ScopeName,
			r.ResourceAttributes, r.Attributes, r.Point, r.ReceivedUnixNano, k.ID})
}

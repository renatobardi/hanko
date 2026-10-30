package ingest

import "math/big"

// metricKinds são os tipos de dado de uma métrica, na ordem em que `otlp.py` os procura, com o nome da coluna.
var metricKinds = []struct{ json, column string }{
	{"gauge", "gauge"}, {"sum", "sum"}, {"histogram", "histogram"},
	{"exponentialHistogram", "exponential_histogram"}, {"summary", "summary"},
}

// MetricRow é uma linha de `telemetry_metrics`, uma por ponto.
type MetricRow struct {
	DedupeKey     string `json:"dedupe_key"`
	TimeUnixNano  int64  `json:"time_unix_nano"`
	StartUnixNano *int64 `json:"start_unix_nano"`
	Fixed
	MetricName             *string  `json:"metric_name"`
	MetricType             string   `json:"metric_type"`
	Unit                   *string  `json:"unit"`
	Value                  *float64 `json:"value"`
	Count                  *int64   `json:"count"`
	IsMonotonic            *bool    `json:"is_monotonic"`
	AggregationTemporality *int32   `json:"aggregation_temporality"`
	ScopeName              *string  `json:"scope_name"`
	ResourceAttributes     string   `json:"resource_attributes"`
	Attributes             string   `json:"attributes"`
	Point                  string   `json:"point"`
	ReceivedUnixNano       int64    `json:"received_unix_nano"`
}

// MetricRows lê um ExportMetricsServiceRequest em JSON. Dedupe por hash do conteúdo (hora em ns, origem, métrica e
// ponto inteiro).
func MetricRows(payload any, received int64) ([]MetricRow, error) {
	rms, err := payloadList(payload, "resourceMetrics")
	if err != nil {
		return nil, err
	}
	var rows []MetricRow
	for _, rm := range rms {
		m, ok := asMap(rm)
		if !ok {
			return nil, bad("resourceMetrics não é objeto")
		}
		res, err := resourceAttrs(m)
		if err != nil {
			return nil, err
		}
		sms, err := listOf(m, "scopeMetrics")
		if err != nil {
			return nil, err
		}
		for _, sm := range sms {
			part, err := metricScope(sm, res, received)
			if err != nil {
				return nil, err
			}
			rows = append(rows, part...)
		}
	}
	return rows, nil
}

func metricScope(sm any, res map[string]any, received int64) ([]MetricRow, error) {
	smm, ok := asMap(sm)
	if !ok {
		return nil, bad("scopeMetrics não é objeto")
	}
	scope, _ := asMap(smm["scope"])
	ml, err := listOf(smm, "metrics")
	if err != nil {
		return nil, err
	}
	ms, err := objects(ml, "metric")
	if err != nil {
		return nil, err
	}
	var rows []MetricRow
	for _, m := range ms {
		part, err := metricPoints(m, scope, res, received)
		if err != nil {
			return nil, err
		}
		rows = append(rows, part...)
	}
	return rows, nil
}

func metricPoints(m, scope, res map[string]any, received int64) ([]MetricRow, error) {
	var kind, column string
	var data map[string]any
	for _, k := range metricKinds {
		if d, ok := asMap(m[k.json]); ok {
			kind, column, data = k.json, k.column, d
			break
		}
	}
	if kind == "" {
		return nil, nil // métrica sem dados: nada a gravar
	}
	pl, err := listOf(data, "dataPoints")
	if err != nil {
		return nil, err
	}
	pts, err := objects(pl, "dataPoint")
	if err != nil {
		return nil, err
	}
	rows := make([]MetricRow, 0, len(pts))
	for _, p := range pts {
		row, err := metricRow(p, m, data, kind, column, scope, res, received)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func metricRow(p, m, data map[string]any, kind, column string, scope, res map[string]any, received int64) (MetricRow, error) {
	pa, err := attrs(p["attributes"])
	if err != nil {
		return MetricRow{}, err
	}
	t, err := toIntOrZero(p["timeUnixNano"])
	if err != nil {
		return MetricRow{}, err
	}
	start, err := toIntOrZero(p["startTimeUnixNano"])
	if err != nil {
		return MetricRow{}, err
	}
	point := map[string]any{}
	for k, v := range p {
		if k != "attributes" {
			point[k] = v
		}
	}
	value, err := metricValue(p)
	if err != nil {
		return MetricRow{}, err
	}
	key := "h:" + contentHash(map[string]any{
		"time": t, "start": start, "resource": res,
		"scope": map[string]any{"name": getKey(scope, "name"), "version": getKey(scope, "version")},
		"name":  m["name"], "type": kind, "unit": m["unit"], "attributes": pa, "point": point,
	})
	at, err := factTime(t, start, received)
	if err != nil {
		return MetricRow{}, err
	}
	startNs, err := ptrInt64OrNil(start, "startTimeUnixNano")
	if err != nil {
		return MetricRow{}, err
	}
	temp, err := toIntOrZero(data["aggregationTemporality"])
	if err != nil {
		return MetricRow{}, err
	}
	agg, err := ptrInt32OrNil(temp, "aggregationTemporality")
	if err != nil {
		return MetricRow{}, err
	}
	row := MetricRow{
		DedupeKey: key, TimeUnixNano: at, StartUnixNano: startNs, Fixed: fixedOf(pa, res),
		MetricName: pyText(m["name"]), MetricType: column, Unit: textOrNil(m["unit"]), Value: value,
		Count: numberInt(p["count"]), AggregationTemporality: agg, ScopeName: textOrNil(getKey(scope, "name")),
		ResourceAttributes: Canon(res), Attributes: Canon(pa), Point: Canon(point), ReceivedUnixNano: received,
	}
	if kind == "sum" {
		row.IsMonotonic = boolOrNil(data["isMonotonic"])
	}
	return row, nil
}

// metricValue: gauge e sum trazem o valor; histogram e summary, a soma (o resto fica em `point`).
func metricValue(p map[string]any) (*float64, error) {
	if v, ok := p["asInt"]; ok {
		n, err := toIntOrZero(v)
		if err != nil {
			return nil, err
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return &f, nil
	}
	if v, ok := p["asDouble"]; ok {
		return numberFloat(v), nil
	}
	return numberFloat(p["sum"]), nil
}

func boolOrNil(v any) *bool {
	if v == nil {
		return nil
	}
	b := truthy(v)
	return &b
}

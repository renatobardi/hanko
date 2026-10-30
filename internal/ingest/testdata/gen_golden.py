"""Gera golden.json a partir do `otlp.py` do agent-studio (oute-agent@845764f), sem tocar no arquivo.

Uso:  python3 -I gen_golden.py <pasta> > golden.json
onde <pasta> tem `agent_studio/__init__.py` vazio, `agent_studio/otlp.py` (copiado de `docker/agent-studio/agent_studio/`
do oute-agent@845764f) e `agent_studio/repo.py` com `def legacy(repo, time_ns, task_id): return repo` (o acerto do
histórico #617/#803 é atribuição inferida da migração, não do dado bruto; ADR-0075 item 7).
"""
import json, sys
sys.path.insert(0, sys.argv[1])
from agent_studio import otlp

RES = {"attributes": [
    {"key": "host.name", "value": {"stringValue": "oute-server"}},
    {"key": "service.name", "value": {"stringValue": "claude-code"}},
    {"key": "oute.agent", "value": {"stringValue": "claude"}},
    {"key": "oute.instance", "value": {"stringValue": "oute-1"}},
    {"key": "n", "value": {"intValue": "42"}},
    {"key": "f", "value": {"doubleValue": 1.5}},
    {"key": "b", "value": {"boolValue": True}},
    {"key": "arr", "value": {"arrayValue": {"values": [{"stringValue": "á\"\n\t "}, {"intValue": "7"}]}}},
    {"key": "kv", "value": {"kvlistValue": {"values": [{"key": "z", "value": {"stringValue": "1"}}, {"key": "a", "value": {"doubleValue": 3.0}}]}}},
    {"key": "raw", "value": {"bytesValue": "AAEC"}},
    {"key": "unk", "value": {"foo": 1}},
]}
LOGS = {"resourceLogs": [{"resource": RES, "scopeLogs": [{"scope": {"name": "oute-emit", "version": "1"}, "logRecords": [
    {"timeUnixNano": "1790000000000000001", "severityNumber": 9, "severityText": "INFO", "body": {"stringValue": "olá"},
     "attributes": [{"key": "oute.event.id", "value": {"stringValue": "e-1"}}, {"key": "session.id", "value": {"stringValue": "s-1"}},
                    {"key": "oute.task.repo", "value": {"stringValue": ""}}, {"key": "event.name", "value": {"stringValue": "oute.canal.proposed"}}],
     "traceId": "0af7651916cd43dd8448eb211c80319c", "spanId": "b7ad6b7169203331"},
    {"timeUnixNano": "1790000000000000002", "observedTimeUnixNano": "1790000000000000003", "severityNumber": 17, "body": {"intValue": "5"},
     "attributes": [{"key": "x", "value": {"doubleValue": 1e20}}, {"key": "y", "value": {"doubleValue": 0.00001}}, {"key": "oute.subscription", "value": {"stringValue": "zai"}}],
     "flags": 1, "eventName": "api_request"},
    {"observedTimeUnixNano": "1790000000000000004", "body": {"kvlistValue": {"values": [{"key": "k", "value": {"boolValue": False}}]}}},
    {"body": {"stringValue": "sem hora"}, "attributes": []},
]}]}]}
SPANS = {"resourceSpans": [{"resource": RES, "scopeSpans": [{"scope": {"name": "claude"}, "spans": [
    {"traceId": "0AF7651916CD43DD8448EB211C80319C", "spanId": "B7AD6B7169203331", "parentSpanId": "00F067AA0BA902B7", "name": "claude_code.llm_request",
     "kind": 3, "startTimeUnixNano": "1790000000000000010", "endTimeUnixNano": "1790000000000000090",
     "status": {"code": 2, "message": "boom"},
     "attributes": [{"key": "gen_ai.response.model", "value": {"stringValue": "claude-opus-5-5"}},
                    {"key": "gen_ai.usage.input_tokens", "value": {"intValue": "100"}}, {"key": "output_tokens", "value": {"intValue": "20"}},
                    {"key": "codex.turn.token_usage.cached_input_tokens", "value": {"intValue": "5"}},
                    {"key": "oute.cost_usd", "value": {"doubleValue": 0.0123}}, {"key": "session.id", "value": {"stringValue": "s-9"}}],
     "events": [{"timeUnixNano": "1790000000000000020", "name": "e", "attributes": [{"key": "a", "value": {"stringValue": "b"}}]}],
     "links": [{"traceId": "aa", "spanId": "bb", "attributes": []}]},
    {"traceId": "11", "spanId": "22", "name": "so"},
]}]}]}
def pt(extra):
    return {"timeUnixNano": "1790000000000000100", "startTimeUnixNano": "1790000000000000000", "attributes": [{"key": "a", "value": {"stringValue": "1"}}], **extra}
METRICS = {"resourceMetrics": [{"resource": RES, "scopeMetrics": [{"scope": {"name": "m", "version": "2"}, "metrics": [
    {"name": "g", "unit": "By", "gauge": {"dataPoints": [pt({"asInt": "7"})]}},
    {"name": "s", "sum": {"isMonotonic": True, "aggregationTemporality": 2, "dataPoints": [pt({"asDouble": 2.5})]}},
    {"name": "h", "histogram": {"aggregationTemporality": 1, "dataPoints": [pt({"count": "3", "sum": 10.0, "bucketCounts": ["1", "2"], "explicitBounds": [1.0]})]}},
    {"name": "vazio"},
]}]}]}

RECV = 1790000000999999999
def clean(rows):
    out = []
    for r in rows:
        r = {k: v for k, v in r.items() if not k.startswith("_")}
        out.append(r)
    return out
gold = {
    "logs": {"payload": LOGS, "rows": clean(otlp.log_rows(LOGS, RECV))},
    "spans": {"payload": SPANS, "rows": clean(otlp.span_rows(SPANS, RECV))},
    "metrics": {"payload": METRICS, "rows": clean(otlp.metric_rows(METRICS, RECV))},
    "received_ns": RECV,
}
json.dump(gold, sys.stdout, ensure_ascii=False, indent=1, sort_keys=True)

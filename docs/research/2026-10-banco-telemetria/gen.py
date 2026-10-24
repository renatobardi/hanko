"""Gera telemetria sintética no schema do agent-studio (store.TABLES, oute-agent @845764f), num DuckDB.

Uso: gen.py <arquivo.duckdb> <dias> [perfil.json]
Volume por dia e formas (tamanhos de JSON, nomes, cardinalidades) vêm do perfil (medido no host, pedido do canal);
sem perfil, os valores de PERFIL abaixo. O fim dos dados é END_NS (2026-10-24T00:00Z) e os dias vão para trás.
"""
import json, sys, time
import duckdb
sys.path.insert(0, "/workspace/oute-agent/docker/agent-studio")
from agent_studio import store  # noqa: E402

END_NS = 1792800000 * 10**9  # 2026-10-24T00:00:00Z
DAY = 86400 * 10**9
PERFIL = {
    # linhas por dia: decisão do Bardi no gate da #67 (ponto 9): 10-06 para spans, 10-12 para logs e metrics
    # formas: perfil medido no host (pedido 20261024-181500 do canal)
    "spans_dia": 134188, "logs_dia": 41006, "metrics_dia": 60927,
    "conversas_dia": 180, "spans_sem_sessao": 0.80,
    "chamadas_frac": 0.037, "tool_frac": 0.03,
    "len": {"logs.body": 47, "logs.attributes": 1192, "logs.resource_attributes": 417,
            "spans.attributes": 411, "spans.resource_attributes": 490, "spans.events": 95,
            "metrics.attributes": 81, "metrics.resource_attributes": 269, "metrics.point": 116},
    "api_request_frac": 0.11,
}


def gen(path, days, p):
    con = duckdb.connect(path)
    con.execute("SET threads = 4; SET memory_limit = '1500MB'; SET preserve_insertion_order = false")
    for t, cols in store.TABLES.items():
        con.execute(f"CREATE TABLE IF NOT EXISTS {t} ({', '.join(f'{c} {ty}' for c, ty in cols)})")
    L = p["len"]
    E = p.get("entropia", 0.3)

    def pad(n):
        """Texto de ~n bytes (0,5n a 1,5n): `m` blocos md5 aleatórios repetidos; `entropia` calibra a compressão."""
        m = max(1, round(n * E / 32))
        blocks = " || ".join(["md5(CAST(random() AS VARCHAR))"] * m)
        return f"substr(repeat({blocks}, {max(1, int(1.5 * n / (32 * m)) + 1)}), 1, greatest(1, CAST({n} * (0.5 + random()) AS INTEGER)))"
    common = """CASE WHEN random() < 0.9 THEN 'oute-server' ELSE 'oute-mac' END AS host_name,
                'oute-agent' AS oute_instance,
                (['claude','codex','human','claude','claude'])[1 + CAST(floor(random() * 5) AS INTEGER)] AS oute_agent,
                (['claude-code','codex_cli_rs','oute','agent-studio'])[1 + CAST(floor(random() * 4) AS INTEGER)] AS service_name,
                {sess} AS session_id,
                CASE WHEN random() < 0.5 THEN 'task-' || CAST(sess_n AS VARCHAR) END AS oute_task_id,
                CASE WHEN random() < 0.2 THEN 'swarm-' || CAST(d AS VARCHAR) || '-' || CAST(sess_n % 3 AS VARCHAR) END AS oute_swarm_round,
                (['oute-agent','oute-agent','oute-agent','frentes-engenharia','lab','hanko','financial-control'])[1 + CAST(floor(random() * 7) AS INTEGER)] AS oute_repo,
                (['claude','claude','zai','codex'])[1 + CAST(floor(random() * 4) AS INTEGER)] AS oute_subscription"""
    res = f"""'{{"host.name":"oute-server","service.name":"claude-code","pad":"' || {pad(L['spans.resource_attributes'] - 60)} || '"}}'"""
    t0 = time.time()
    for k in range(days):
        d = (END_NS - (k + 1) * DAY) // DAY  # número do dia
        base = d * DAY
        S, Lg, M = p["spans_dia"], p["logs_dia"], p["metrics_dia"]
        nconv = p["conversas_dia"]
        sess = (f"CASE WHEN random() < {p['spans_sem_sessao']} THEN NULL ELSE "
                f"'s-' || CAST({d} AS VARCHAR) || '-' || CAST(sess_n AS VARCHAR) END")
        con.execute(f"""INSERT INTO spans
            SELECT 's:' || CAST({d} AS VARCHAR) || ':' || CAST(i AS VARCHAR), make_timestamp(CAST(tn // 1000 AS BIGINT)), tn,
                   tn + dur, dur, {common.format(sess=sess)},
                   md5(CAST({d} * 100000 + sess_n * 100 + i % 37 AS VARCHAR)), substr(md5(CAST(i AS VARCHAR) || '{d}'), 1, 16),
                   substr(md5(CAST(i - 1 AS VARCHAR) || '{d}'), 1, 16),
                   CASE WHEN r < {p['chamadas_frac']} THEN 'claude_code.llm_request'
                        WHEN r < {p['chamadas_frac'] + p['tool_frac']} THEN 'claude_code.tool'
                        ELSE 'op.' || CAST(CAST(floor(random() * 40) AS INTEGER) AS VARCHAR) END,
                   1, CASE WHEN random() < 0.01 THEN 2 ELSE 0 END, NULL,
                   CASE WHEN r < {p['chamadas_frac']} THEN (['claude-sonnet-4-5','claude-opus-4-1','claude-haiku-4-5','gpt-5-codex','glm-4.6','gpt-5','claude-sonnet-4','o3'])[1 + CAST(floor(random() * 8) AS INTEGER)] END,
                   CASE WHEN r < {p['chamadas_frac']} THEN CAST(random() * 20000 AS BIGINT) END,
                   CASE WHEN r < {p['chamadas_frac']} THEN CAST(random() * 3000 AS BIGINT) END,
                   CASE WHEN r < {p['chamadas_frac']} THEN CAST(random() * 80000 AS BIGINT) END,
                   CASE WHEN r < {p['chamadas_frac']} THEN CAST(random() * 5000 AS BIGINT) END,
                   CASE WHEN r < {p['chamadas_frac']} THEN random() * 0.2 END,
                   'scope', {res},
                   '{{"request_id":"req_' || CAST({d} AS VARCHAR) || '_' || CAST(i AS VARCHAR) || '","tool_name":"' ||
                   (['Bash','Read','Edit','Grep','Write','Glob','Task','WebFetch'])[1 + CAST(floor(random() * 8) AS INTEGER)] ||
                   '","bash_command_class":"vcs","pad":"' || {pad(L['spans.attributes'] - 110)} || '"}}',
                   '[{{"name":"e","pad":"' || {pad(L['spans.events'] - 25)} || '"}}]', NULL,
                   make_timestamp(CAST(tn // 1000 AS BIGINT)), tn + 1000000
            FROM (SELECT i, random() AS r, {base} + CAST(random() * {DAY} AS BIGINT) AS tn,
                         CAST(random() * 5e9 AS BIGINT) AS dur, CAST(floor(random() * {nconv}) AS INTEGER) AS sess_n, {d} AS d
                  FROM range({S}) t(i))""")
        sess_l = f"'s-' || CAST({d} AS VARCHAR) || '-' || CAST(sess_n AS VARCHAR)"
        con.execute(f"""INSERT INTO logs
            SELECT 'ev:' || CAST({d} AS VARCHAR) || ':' || CAST(i AS VARCHAR), make_timestamp(CAST(tn // 1000 AS BIGINT)), tn, tn,
                   {common.format(sess=sess_l)},
                   CASE WHEN r < {p['api_request_frac']} THEN 'api_request'
                        ELSE (['tool_result','user_prompt','tool_decision','oute.task.opened','oute.swarm.watch.ci','oute.canal.snapshot','api_error'])[1 + CAST(floor(random() * 7) AS INTEGER)] END,
                   'ev-' || CAST({d} AS VARCHAR) || '-' || CAST(i AS VARCHAR),
                   CASE WHEN random() < 0.01 THEN 17 ELSE 9 END, 'INFO', {pad(L['logs.body'])}, NULL, NULL, 'scope',
                   '{{"host.name":"oute-server","pad":"' || {pad(L['logs.resource_attributes'] - 40)} || '"}}',
                   '{{"request_id":"req_' || CAST({d} AS VARCHAR) || '_' || CAST(CAST(floor(random() * {S}) AS BIGINT) AS VARCHAR) ||
                   '","cost_usd":"' || CAST(round(random() * 0.2, 6) AS VARCHAR) || '","query_source":"main","pad":"' ||
                   {pad(L['logs.attributes'] - 110)} || '"}}',
                   make_timestamp(CAST(tn // 1000 AS BIGINT)), tn + 1000000
            FROM (SELECT i, random() AS r, {base} + CAST(random() * {DAY} AS BIGINT) AS tn,
                         CAST(floor(random() * {nconv}) AS INTEGER) AS sess_n, {d} AS d FROM range({Lg}) t(i))""")
        con.execute(f"""INSERT INTO metrics
            SELECT 'h:' || CAST({d} AS VARCHAR) || ':' || CAST(i AS VARCHAR), make_timestamp(CAST(tn // 1000 AS BIGINT)), tn, tn - 60000000000,
                   {common.format(sess="CASE WHEN random() < 0.4 THEN NULL ELSE 's-' || CAST(d AS VARCHAR) || '-' || CAST(sess_n AS VARCHAR) END")},
                   (['otelcol_exporter_queue_size','otelcol_exporter_queue_capacity','otelcol_exporter_send_failed_spans',
                     'claude_code.token.usage','claude_code.cost.usage','oute.quota.used','oute.emit.spool.bytes',
                     'otelcol_process_memory_rss','claude_code.session.count','oute.memory.capture.ratio'])[1 + CAST(floor(random() * 10) AS INTEGER)],
                   'sum', '1', random() * 1000, NULL, TRUE, 2, 'scope',
                   '{{"host.name":"oute-server","pad":"' || {pad(L['metrics.resource_attributes'] - 40)} || '"}}',
                   '{{"exporter":"otlp_http/studio_logs","pad":"' || {pad(L['metrics.attributes'] - 40)} || '"}}',
                   '{{"pad":"' || {pad(L['metrics.point'] - 12)} || '"}}',
                   make_timestamp(CAST(tn // 1000 AS BIGINT)), tn + 1000000
            FROM (SELECT i, {base} + CAST(random() * {DAY} AS BIGINT) AS tn,
                         CAST(floor(random() * {nconv}) AS INTEGER) AS sess_n, {d} AS d FROM range({M}) t(i))""")
        if (k + 1) % 30 == 0 or k + 1 == days:
            con.execute("CHECKPOINT")
            print(f"dia {k + 1}/{days} em {time.time() - t0:.0f} s", flush=True)
    con.close()


if __name__ == "__main__":
    p = dict(PERFIL)
    if len(sys.argv) > 3:
        p.update(json.load(open(sys.argv[3])))
    gen(sys.argv[1], int(sys.argv[2]), p)

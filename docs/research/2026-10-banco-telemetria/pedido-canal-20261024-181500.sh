set -euo pipefail
# RESUMO
# Faz: para a pesquisa de banco (#68 do Hanko), só leitura: (1) CPU, RAM e limite de memória do container; (2) tempo das
#      APIs de leitura do agent-studio vivo (usage 24 h a 366 dias, tray, alerts), 2 vezes cada, com 7 s entre elas;
#      (3) cópia consistente do DuckDB pelo serviço (o mesmo `python -m agent_studio.backup` do backup de sempre), perfil
#      da cópia (tamanhos de JSON, nomes, cardinalidades) e tempo de 10 consultas em read_only; (4) apaga a cópia.
# Host: oute-mac; os passos rodam no oute-server por `ssh oute-server` (usuário ubuntu, sem sudo).
# Altera: cria um arquivo temporário de ~2,5 GB em /data/agent-studio/backup/ (74 GB livres em 2026-10-24) e o apaga no passo 4.
# Não toca: o DuckDB vivo (a ingestão segue), o SurrealDB, o bucket, o collector. As APIs são só GET.
# Reinicia: nada.
# Saída: números, nomes de evento, de span e de métrica. Nenhum corpo, prompt, atributo ou id de conversa. A credencial de
#        leitura é lida do ambiente do próprio container e nunca é impressa.
ssh -o BatchMode=yes -o ConnectTimeout=10 oute-server 'bash -s' <<'REMOTE'
set -euo pipefail
C=oute-agent-studio
PY=/opt/agent-studio/venv/bin/python
echo "1/4: CPU, RAM e limite do container"
nproc; free -m | head -2
docker inspect -f 'limite de memória do container (bytes): {{.HostConfig.Memory}}' "$C"
docker stats --no-stream --format 'uso agora: {{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}}' "$C" oute-surrealdb
if docker exec "$C" sh -c 'ls /data/agent-studio/backup/agent-studio-*.duckdb' >/dev/null 2>&1; then
  echo "já há cópia na pasta backup/ (outro backup em andamento?): paro sem mudar nada"; exit 1
fi

echo "2/4: tempo das APIs de leitura (serviço vivo)"
docker exec -i "$C" "$PY" - <<'PYAPI'
# tempos das APIs de leitura do agent-studio vivo, de dentro do container; a credencial vem do ambiente e não é impressa
import os, time, urllib.request
tok = os.environ.get("AGENT_STUDIO_READ_TOKEN", "")
if not tok:
    print("sem credencial de leitura no ambiente do container: passo pulado"); raise SystemExit(0)
for path in ("/v1/usage?hours=24", "/v1/usage?hours=168", "/v1/usage?hours=720", "/v1/usage?hours=8784",
             "/v1/tray", "/v1/alerts"):
    out = []
    for i in range(2):
        req = urllib.request.Request("http://127.0.0.1:8430" + path, headers={"Authorization": "Bearer " + tok})
        t0 = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                n = len(r.read()); st = r.status
        except Exception as e:
            n, st = 0, type(e).__name__
        out.append(f"{(time.perf_counter() - t0) * 1000:.0f}ms/{st}/{n}B")
        time.sleep(7)  # acima do cache de leitura (AGENT_STUDIO_READ_TTL_S=5)
    print(path, *out, sep="\t")
PYAPI

# CUIDADO: o passo 3 grava uma cópia de ~2,5 GB em /data/agent-studio/backup/ e segura a trava de cópia enquanto copia (um `oute studio backup` no mesmo instante recebe 409 e tenta depois). Nada do banco vivo se perde.
echo "3/4: cópia consistente, perfil e tempos das consultas (read_only)"
out="$(docker exec "$C" "$PY" -m agent_studio.backup)"
echo "$out"
file="$(printf '%s' "$out" | sed -n 's/.*"file": *"\(agent-studio-[0-9]\{8\}T[0-9]\{6\}Z\.duckdb\)".*/\1/p')"
[ -n "$file" ] || { echo "resposta sem nome de arquivo no formato esperado: paro"; exit 1; }
rc=0
docker exec -i -e F="/data/agent-studio/backup/$file" -e MEM=2GB "$C" "$PY" - <<'PYQ' || rc=$?
"""Conjunto de consultas da #68: as leituras que as telas e os consumidores do agent-studio fazem, reduzidas ao SQL
que pesa (janela, escopo de chamada ao modelo, junção do custo real, agrupamento por conversa). Dois dialetos: DuckDB
(o mesmo SQL roda no banco real do host e no sintético local) e Postgres. Janela relativa a `now_ns` (o fim dos dados).

Fonte de cada consulta (oute-agent @845764f, docker/agent-studio/agent_studio/):
  q1 usage.py:145-172 + cost.py:39-47      uso por host × agente × modelo, 7 dias, com p95
  q2 cost.py:229-251                       custo real: span de chamada + log api_request pelo request_id, 7 dias
  q3 dashboard.py (série por dia)          série diária de 30 dias
  q4 usage.py (janela de 366 dias)         série diária de 366 dias
  q5 conversations.py:27-89                lista de conversas, 7 dias, 50 mais novas
  q6 conversations.py:158-213              detalhe: spans (até 5000) e 1ª página de logs (200) da maior conversa
  q7 tray.py:73-104                        Tray: custo de hoje por agente e erros da última hora
  q8 alerts.py:197-311                     alertas: último dado por host (24 h) e métricas de exportação (15 min)
  q9 tools.py:56-70                        ferramentas: usos por tool_name, 7 dias (JSON em muitas linhas)
  q10 dashboard.py (heatmap)               dia da semana × hora, 7 dias
"""
H = 3_600_000_000_000
D = 24 * H

EXPORT_METRICS = ("'otelcol_exporter_queue_size','otelcol_exporter_queue_capacity','otelcol_exporter_send_failed_spans',"
                  "'otelcol_exporter_send_failed_metric_points','otelcol_exporter_send_failed_log_records'")
CALL = ("name IN ('claude_code.llm_request','session_task.turn','ai_memory.llm_request','jev.decision') "
        "AND (name = 'jev.decision' OR oute_agent IS DISTINCT FROM 'router')")


def _duck(now):
    w7, w30, w366, today, h1 = now - 7 * D, now - 30 * D, now - 366 * D, now - (now % D), now - H
    j = lambda col, key: f"json_extract_string({col}, '$.\"{key}\"')"
    day = "CAST(time_unix_nano // 86400000000000 AS BIGINT)"
    return {
        "q1": f"""SELECT host_name, oute_agent, model, count(*), sum(input_tokens), sum(output_tokens),
                  sum(cache_read_tokens), sum(cache_creation_tokens), sum(cost_usd),
                  quantile_cont(CAST(duration_ns AS DOUBLE), 0.95)
                  FROM spans WHERE time_unix_nano >= {w7} AND time_unix_nano < {now} AND {CALL} GROUP BY ALL""",
        "q2": f"""SELECT s.model, count(*), sum(coalesce(l.cost, s.cost_usd)) FROM
                  (SELECT model, cost_usd, {j('attributes', 'request_id')} AS rid FROM spans
                   WHERE time_unix_nano >= {w7} AND time_unix_nano < {now} AND {CALL}) s
                  LEFT JOIN (SELECT {j('attributes', 'request_id')} AS rid,
                             max(TRY_CAST({j('attributes', 'cost_usd')} AS DOUBLE)) AS cost FROM logs
                             WHERE event_name IN ('api_request','claude_code.api_request')
                             AND time_unix_nano >= {w7 - H} AND time_unix_nano < {now + H} GROUP BY 1) l
                  ON l.rid = s.rid GROUP BY 1""",
        "q3": f"""SELECT {day} AS d, count(*), sum(cost_usd), sum(input_tokens + output_tokens) FROM spans
                  WHERE time_unix_nano >= {w30} AND time_unix_nano < {now} AND {CALL} GROUP BY 1 ORDER BY 1""",
        "q4": f"""SELECT {day} AS d, count(*), sum(cost_usd), sum(input_tokens + output_tokens) FROM spans
                  WHERE time_unix_nano >= {w366} AND time_unix_nano < {now} AND {CALL} GROUP BY 1 ORDER BY 1""",
        "q5": f"""WITH f AS (SELECT session_id, host_name, oute_agent, time_unix_nano AS t,
                             coalesce(end_unix_nano, time_unix_nano) AS e, TRUE AS sp FROM spans
                             WHERE session_id IS NOT NULL AND time_unix_nano >= {w7} AND time_unix_nano < {now}
                             UNION ALL SELECT session_id, host_name, oute_agent, time_unix_nano, time_unix_nano, FALSE
                             FROM logs WHERE session_id IS NOT NULL AND time_unix_nano >= {w7} AND time_unix_nano < {now})
                  SELECT session_id, min(t) AS s, max(e), arg_min(host_name, t), arg_min(oute_agent, t),
                         count(*) FILTER (WHERE sp), count(*) FILTER (WHERE NOT sp)
                  FROM f GROUP BY session_id ORDER BY s DESC, session_id LIMIT 50""",
        "q6_pick": f"""SELECT session_id FROM spans WHERE session_id IS NOT NULL AND time_unix_nano >= {w7}
                       GROUP BY 1 ORDER BY count(*) DESC LIMIT 1""",
        "q6": """SELECT trace_id, span_id, parent_span_id, name, time_unix_nano, duration_ns, model, input_tokens,
                 output_tokens, cost_usd, status_code FROM spans WHERE session_id = '{sid}'
                 ORDER BY time_unix_nano, trace_id, span_id LIMIT 5000;
                 SELECT time_unix_nano, severity_number, event_name, body, attributes FROM logs
                 WHERE session_id = '{sid}' ORDER BY time_unix_nano LIMIT 200""",
        "q7": f"""SELECT oute_agent, count(*), sum(cost_usd) FROM spans
                  WHERE time_unix_nano >= {today} AND time_unix_nano < {now} AND {CALL} GROUP BY 1;
                  SELECT (SELECT count(*) FROM spans WHERE time_unix_nano >= {h1} AND status_code = 2),
                         (SELECT count(*) FROM logs WHERE time_unix_nano >= {h1} AND severity_number >= 17)""",
        "q8": f"""SELECT host_name, max(t) FROM (
                    SELECT host_name, max(time_unix_nano) AS t FROM logs WHERE time_unix_nano >= {now - D} GROUP BY 1
                    UNION ALL SELECT host_name, max(time_unix_nano) FROM spans WHERE time_unix_nano >= {now - D} GROUP BY 1
                    UNION ALL SELECT host_name, max(time_unix_nano) FROM metrics WHERE time_unix_nano >= {now - D} GROUP BY 1)
                  GROUP BY 1;
                  SELECT host_name, {j('attributes', 'exporter')}, metric_name, max(value) FROM metrics
                  WHERE metric_name IN ({EXPORT_METRICS})
                  AND time_unix_nano BETWEEN {now - H // 4} AND {now} GROUP BY ALL""",
        "q9": f"""SELECT coalesce(nullif({j('attributes', 'tool_name')}, ''), '(sem nome)'), count(*),
                  count(*) FILTER (WHERE status_code = 2) FROM spans
                  WHERE name = 'claude_code.tool' AND time_unix_nano >= {w7} AND time_unix_nano < {now} GROUP BY 1""",
        "q10": f"""SELECT dayofweek(make_timestamp(CAST(time_unix_nano // 1000 AS BIGINT))) AS dw,
                   hour(make_timestamp(CAST(time_unix_nano // 1000 AS BIGINT))) AS h, count(*), sum(cost_usd) FROM spans
                   WHERE time_unix_nano >= {w7} AND time_unix_nano < {now} AND {CALL} GROUP BY 1, 2""",
    }


def _pg(now):
    """O mesmo, no Postgres: `->>` no lugar de json_extract_string, percentile_cont, DISTINCT ON para arg_min."""
    q = _duck(now)
    w7, w30, w366, today, h1 = now - 7 * D, now - 30 * D, now - 366 * D, now - (now % D), now - H
    j = lambda col, key: f"({col}->>'{key}')"
    day = "(time_unix_nano / 86400000000000)"
    q["q1"] = f"""SELECT host_name, oute_agent, model, count(*), sum(input_tokens), sum(output_tokens),
                  sum(cache_read_tokens), sum(cache_creation_tokens), sum(cost_usd),
                  percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns::float8)
                  FROM spans WHERE time_unix_nano >= {w7} AND time_unix_nano < {now} AND {CALL} GROUP BY 1, 2, 3"""
    q["q2"] = f"""SELECT s.model, count(*), sum(coalesce(l.cost, s.cost_usd)) FROM
                  (SELECT model, cost_usd, {j('attributes', 'request_id')} AS rid FROM spans
                   WHERE time_unix_nano >= {w7} AND time_unix_nano < {now} AND {CALL}) s
                  LEFT JOIN (SELECT {j('attributes', 'request_id')} AS rid,
                             max(CASE WHEN {j('attributes', 'cost_usd')} ~ '^-?[0-9.eE+-]+$'
                                      THEN {j('attributes', 'cost_usd')}::float8 END) AS cost FROM logs
                             WHERE event_name IN ('api_request','claude_code.api_request')
                             AND time_unix_nano >= {w7 - H} AND time_unix_nano < {now + H} GROUP BY 1) l
                  ON l.rid = s.rid GROUP BY 1"""
    q["q3"] = q["q3"].replace("CAST(time_unix_nano // 86400000000000 AS BIGINT)", day)
    q["q4"] = q["q4"].replace("CAST(time_unix_nano // 86400000000000 AS BIGINT)", day)
    q["q5"] = f"""WITH f AS (SELECT session_id, host_name, oute_agent, time_unix_nano AS t,
                             coalesce(end_unix_nano, time_unix_nano) AS e, TRUE AS sp FROM spans
                             WHERE session_id IS NOT NULL AND time_unix_nano >= {w7} AND time_unix_nano < {now}
                             UNION ALL SELECT session_id, host_name, oute_agent, time_unix_nano, time_unix_nano, FALSE
                             FROM logs WHERE session_id IS NOT NULL AND time_unix_nano >= {w7} AND time_unix_nano < {now}),
                  g AS (SELECT session_id, min(t) AS s, max(e) AS e, count(*) FILTER (WHERE sp) AS ns,
                               count(*) FILTER (WHERE NOT sp) AS nl FROM f GROUP BY session_id
                        ORDER BY s DESC, session_id LIMIT 50)
                  SELECT g.*, (SELECT host_name FROM f WHERE f.session_id = g.session_id ORDER BY t LIMIT 1),
                         (SELECT oute_agent FROM f WHERE f.session_id = g.session_id ORDER BY t LIMIT 1)
                  FROM g ORDER BY s DESC, session_id"""
    q["q8"] = f"""SELECT host_name, max(t) FROM (
                    SELECT host_name, max(time_unix_nano) AS t FROM logs WHERE time_unix_nano >= {now - D} GROUP BY 1
                    UNION ALL SELECT host_name, max(time_unix_nano) FROM spans WHERE time_unix_nano >= {now - D} GROUP BY 1
                    UNION ALL SELECT host_name, max(time_unix_nano) FROM metrics WHERE time_unix_nano >= {now - D} GROUP BY 1) x
                  GROUP BY 1;
                  SELECT host_name, {j('attributes', 'exporter')}, metric_name, max(value) FROM metrics
                  WHERE metric_name IN ({EXPORT_METRICS})
                  AND time_unix_nano BETWEEN {now - H // 4} AND {now} GROUP BY 1, 2, 3"""
    q["q9"] = q["q9"].replace(j_duck("attributes", "tool_name"), j("attributes", "tool_name"))
    q["q10"] = f"""SELECT extract(dow FROM to_timestamp(time_unix_nano / 1e9)) AS dw,
                   extract(hour FROM to_timestamp(time_unix_nano / 1e9) AT TIME ZONE 'UTC') AS h, count(*), sum(cost_usd)
                   FROM spans WHERE time_unix_nano >= {w7} AND time_unix_nano < {now} AND {CALL} GROUP BY 1, 2"""
    return q


def j_duck(col, key):
    return f"json_extract_string({col}, '$.\"{key}\"')"


def queries(dialect, now):
    return _duck(now) if dialect == "duckdb" else _pg(now)
# --- corredor da #68 no host (anexado depois do qset.py): perfil da cópia e tempos do conjunto de consultas
import os, time, statistics, duckdb
con = duckdb.connect(os.environ["F"], read_only=True, config={"memory_limit": os.environ.get("MEM", "2GB"), "threads": "2"})
def rows(sql):
    return con.execute(sql).fetchall()
def show(title, sql):
    print("--", title)
    try:
        for r in rows(sql):
            print(" ", *r, sep="\t")
    except Exception as e:
        print("  (não medido:", type(e).__name__, str(e)[:160], ")")
now = rows("SELECT max(time_unix_nano) FROM spans")[0][0]
print("now_ns", now)
print("== perfil")
for t, cols in (("logs", ("body", "attributes", "resource_attributes")),
                ("spans", ("attributes", "resource_attributes", "events", "links")),
                ("metrics", ("attributes", "resource_attributes", "point"))):
    for c in cols:
        show(f"{t}.{c}: média, p50, p95, máx (bytes)",
             f"SELECT round(avg(length(coalesce({c}::VARCHAR,'')))), quantile_disc(length(coalesce({c}::VARCHAR,'')), 0.5), "
             f"quantile_disc(length(coalesce({c}::VARCHAR,'')), 0.95), max(length(coalesce({c}::VARCHAR,''))) FROM {t}")
    show(f"{t}: conversas e traces distintos por dia (média dos dias desde 2026-10-11)",
         f"SELECT round(avg(c)), round(avg(n)) FROM (SELECT time_unix_nano // 86400000000000 d, count(DISTINCT session_id) c, count(*) n "
         f"FROM {t} WHERE time_unix_nano >= 1760140800000000000 GROUP BY 1)")
show("spans: sem session_id (fração)", "SELECT round(avg(CASE WHEN session_id IS NULL THEN 1 ELSE 0 END), 3) FROM spans")
show("spans desde 2026-10-11: 30 nomes", "SELECT name, count(*) FROM spans WHERE time_unix_nano >= 1760140800000000000 GROUP BY 1 ORDER BY 2 DESC LIMIT 30")
show("logs: 30 event_name", "SELECT event_name, count(*) FROM logs GROUP BY 1 ORDER BY 2 DESC LIMIT 30")
show("metrics: 30 metric_name", "SELECT metric_name, metric_type, count(*) FROM metrics GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 30")
show("spans: chamadas ao modelo por dia (últimos 14)", f"SELECT time_unix_nano // 86400000000000 d, count(*) FROM spans WHERE {CALL} GROUP BY 1 ORDER BY 1 DESC LIMIT 14")
show("modelos distintos; traces distintos", "SELECT count(DISTINCT model), count(DISTINCT trace_id) FROM spans")
show("tamanho do arquivo da cópia", "SELECT database_size, block_size, total_blocks, used_blocks FROM pragma_database_size()")
print("== tempos (ms; 1ª execução e mediana de 3 seguintes)")
q = queries("duckdb", now)
sid = rows(q["q6_pick"])[0][0]
for k in sorted((k for k in q if k != "q6_pick"), key=lambda k: int(k[1:])):
    sqls = [s.replace("{sid}", sid) for s in q[k].split(";") if s.strip()]
    ts = []
    for i in range(4):
        t0 = time.perf_counter()
        n = 0
        for s in sqls:
            n += len(con.execute(s).fetchall())
        ts.append((time.perf_counter() - t0) * 1000)
    print(k, f"1a={ts[0]:.0f}", f"mediana={statistics.median(ts[1:]):.0f}", f"linhas={n}", sep="\t")
con.close()
PYQ

# CUIDADO: o passo 4 apaga a cópia temporária criada no passo 3 (só o arquivo com o nome que o serviço devolveu). O banco vivo não muda; o backup de sempre segue no bucket.
echo "4/4: apaga a cópia temporária"
docker exec "$C" rm -f "/data/agent-studio/backup/$file" "/data/agent-studio/backup/$file.wal"
docker exec "$C" ls -la /data/agent-studio/backup
exit "$rc"
REMOTE

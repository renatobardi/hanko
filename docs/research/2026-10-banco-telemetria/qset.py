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
    q["q5"] = f"""WITH f AS MATERIALIZED (SELECT session_id, host_name, oute_agent, time_unix_nano AS t,
                             coalesce(end_unix_nano, time_unix_nano) AS e, TRUE AS sp FROM spans
                             WHERE session_id IS NOT NULL AND time_unix_nano >= {w7} AND time_unix_nano < {now}
                             UNION ALL SELECT session_id, host_name, oute_agent, time_unix_nano, time_unix_nano, FALSE
                             FROM logs WHERE session_id IS NOT NULL AND time_unix_nano >= {w7} AND time_unix_nano < {now}),
                  g AS (SELECT session_id, min(t) AS s, max(e) AS e, count(*) FILTER (WHERE sp) AS ns,
                               count(*) FILTER (WHERE NOT sp) AS nl FROM f GROUP BY session_id
                        ORDER BY s DESC, session_id LIMIT 50),
                  fst AS (SELECT DISTINCT ON (session_id) session_id, host_name, oute_agent FROM f
                          WHERE session_id IN (SELECT session_id FROM g) ORDER BY session_id, t)
                  SELECT g.*, fst.host_name, fst.oute_agent FROM g JOIN fst USING (session_id)
                  ORDER BY s DESC, session_id"""
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

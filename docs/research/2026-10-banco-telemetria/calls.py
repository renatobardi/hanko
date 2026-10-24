"""Variante do Postgres com tabela estreita de chamadas ao modelo (o recorte que uso, custo e Dashboard leem) e
agregado diário. Mede q1, q3, q4 e q10 contra ela, no schema m12part. Tempo: 1ª e mediana de 3, em ms."""
import statistics, time, psycopg, qset
c = psycopg.connect("host=127.0.0.1 port=56432 user=bench dbname=tele", autocommit=True)
c.execute("SET search_path = m12part")
t0 = time.time()
for s in (f"DROP TABLE IF EXISTS model_calls",
          f"""CREATE TABLE model_calls AS SELECT time_unix_nano, host_name, oute_agent, model, oute_repo, oute_subscription,
              session_id, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_usd, duration_ns,
              attributes->>'request_id' AS request_id FROM spans WHERE {qset.CALL}""",
          "CREATE INDEX ON model_calls (time_unix_nano)", "VACUUM ANALYZE model_calls",
          """DROP TABLE IF EXISTS calls_daily""",
          """CREATE TABLE calls_daily AS SELECT time_unix_nano / 86400000000000 AS d, host_name, oute_agent, model, oute_repo,
              oute_subscription, count(*) AS n, sum(input_tokens) AS it, sum(output_tokens) AS ot, sum(cost_usd) AS cost
              FROM model_calls GROUP BY 1, 2, 3, 4, 5, 6""",
          "CREATE INDEX ON calls_daily (d)", "VACUUM ANALYZE calls_daily"):
    c.execute(s)
print(f"construção: {time.time() - t0:.0f} s")
for t in ("model_calls", "calls_daily"):
    n, sz = c.execute(f"SELECT count(*), pg_total_relation_size('{t}') FROM {t}").fetchone()
    print(t, n, "linhas", sz, "bytes")
now = c.execute("SELECT max(time_unix_nano) FROM spans").fetchone()[0]
D = qset.D
Q = {"q1": f"""SELECT host_name, oute_agent, model, count(*), sum(input_tokens), sum(output_tokens), sum(cost_usd),
               percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ns::float8) FROM model_calls
               WHERE time_unix_nano >= {now - 7 * D} AND time_unix_nano < {now} GROUP BY 1, 2, 3""",
     "q3": f"""SELECT time_unix_nano / 86400000000000, count(*), sum(cost_usd), sum(input_tokens + output_tokens) FROM model_calls
               WHERE time_unix_nano >= {now - 30 * D} AND time_unix_nano < {now} GROUP BY 1 ORDER BY 1""",
     "q4": f"""SELECT time_unix_nano / 86400000000000, count(*), sum(cost_usd), sum(input_tokens + output_tokens) FROM model_calls
               WHERE time_unix_nano >= {now - 366 * D} AND time_unix_nano < {now} GROUP BY 1 ORDER BY 1""",
     "q4_agregado": f"""SELECT d, sum(n), sum(cost), sum(it + ot) FROM calls_daily WHERE d >= {(now - 366 * D) // D} GROUP BY 1 ORDER BY 1""",
     "q10": f"""SELECT extract(dow FROM to_timestamp(time_unix_nano / 1e9)), extract(hour FROM to_timestamp(time_unix_nano / 1e9)),
                count(*), sum(cost_usd) FROM model_calls WHERE time_unix_nano >= {now - 7 * D} AND time_unix_nano < {now} GROUP BY 1, 2"""}
for k, q in Q.items():
    ts = []
    for _ in range(4):
        t0 = time.perf_counter(); n = len(c.execute(q).fetchall()); ts.append((time.perf_counter() - t0) * 1000)
    print(k, f"1a={ts[0]:.0f}", f"mediana={statistics.median(ts[1:]):.0f}", f"linhas={n}", sep="\t")

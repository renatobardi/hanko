"""Vazão de ingestão com dedupe, no volume já carregado: lotes de 1000 spans novos + 10% repetidos (o reenvio do #570).

Uso: ingest.py duckdb <arquivo.duckdb> | ingest.py pg <schema>
DuckDB: INSERT OR IGNORE (a PK do agent-studio, como o store faz); Postgres: INSERT … ON CONFLICT DO NOTHING com COPY
para uma tabela temporária. 20 lotes; imprime linhas/s (mediana) e o tempo do lote mais lento.
grava no próprio banco de teste (dedupe_key 'ing:…', na última hora dos dados). Rodar depois
das consultas.
"""
import statistics, sys, time

N, LOTES = 1000, 20


def rows(k, now):
    out = []
    for i in range(N):
        key = f"ing:{k - 1 if (i < N // 10 and k > 0) else k}:{i}"  # os 10% primeiros repetem o lote anterior
        t = now - 3600 * 10**9 + k * N + i  # na última hora: cai na partição existente
        out.append((key, t, t + 1000, 1000, "oute-server", "oute-agent", "claude", "claude-code", f"s-ing-{k % 7}", None, None,
                    "oute-agent", "claude", f"tr{k}", f"sp{i}", None, "claude_code.tool", 1, 0, None, None, None, None, None,
                    None, None, "scope", '{"host.name":"oute-server"}', '{"tool_name":"Bash","pad":"' + "y" * 380 + '"}',
                    "[]", None, t + 5))
    return out


COLS = ("dedupe_key, time_unix_nano, end_unix_nano, duration_ns, host_name, oute_instance, oute_agent, service_name, session_id, "
        "oute_task_id, oute_swarm_round, oute_repo, oute_subscription, trace_id, span_id, parent_span_id, name, kind, "
        "status_code, status_message, model, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_usd, "
        "scope_name, resource_attributes, attributes, events, links, received_unix_nano")


def main():
    kind, target = sys.argv[1], sys.argv[2]
    ts = []
    if kind == "duckdb":
        import duckdb
        con = duckdb.connect(target, config={"memory_limit": "2GB", "threads": "2"})
        now = con.execute("SELECT max(time_unix_nano) FROM spans WHERE dedupe_key NOT LIKE 'ing:%'").fetchone()[0]
        ph = ", ".join(["?"] * 32)
        for k in range(LOTES):
            cols = list(zip(*rows(k, now)))  # uma lista por coluna, como o store (store.py:139-148: unnest, uma instrução por lote)
            names = [c.strip() for c in COLS.split(",")]
            sel = ", ".join(f"unnest(?) AS {n}" for n in names)
            t0 = time.perf_counter()
            con.execute("BEGIN")
            con.execute(f"INSERT OR IGNORE INTO spans (time, received_at, {COLS}) SELECT make_timestamp(CAST(time_unix_nano // 1000 AS BIGINT)), "
                        f"make_timestamp(CAST(received_unix_nano // 1000 AS BIGINT)), {COLS} FROM (SELECT {sel})", [list(c) for c in cols])
            con.execute("COMMIT")
            ts.append(time.perf_counter() - t0)
    else:
        import psycopg
        con = psycopg.connect("host=127.0.0.1 port=56432 user=bench dbname=tele")
        con.execute(f"SET search_path = {target}")
        now = con.execute("SELECT max(time_unix_nano) FROM spans WHERE dedupe_key NOT LIKE 'ing:%'").fetchone()[0]
        con.commit()
        for k in range(LOTES):
            batch = rows(k, now)
            t0 = time.perf_counter()
            with con.cursor() as cur:
                cur.execute("CREATE TEMP TABLE IF NOT EXISTS ing ON COMMIT DELETE ROWS AS SELECT * FROM spans WITH NO DATA")
                with cur.copy(f"COPY ing ({COLS}) FROM STDIN") as cp:
                    for r in batch:
                        cp.write_row(r)
                cur.execute(f"""INSERT INTO spans ({COLS}, time, received_at)
                    SELECT {COLS}, to_timestamp(time_unix_nano / 1e9), to_timestamp(received_unix_nano / 1e9) FROM ing
                    ON CONFLICT DO NOTHING""")
            con.commit()
            ts.append(time.perf_counter() - t0)
    rate = [N / t for t in ts]
    print(f"{kind} {target}: {statistics.median(rate):.0f} linhas/s (mediana de {LOTES} lotes de {N}), "
          f"lote mais lento {max(ts) * 1000:.0f} ms")


if __name__ == "__main__":
    main()

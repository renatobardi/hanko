"""Roda o conjunto de consultas (qset.py) num DuckDB ou num schema do Postgres e imprime tempo e tamanho.

Uso: bench.py duckdb <arquivo.duckdb> [memória]   |   bench.py pg <schema>
Tempo: 1ª execução e mediana das 3 seguintes, em ms. `now` = fim dos dados (max time_unix_nano de spans).
"""
import os, statistics, sys, time
import qset

DSN = os.environ.get("PGDSN", "host=127.0.0.1 port=56432 user=bench dbname=tele")


def run(execute, q):
    sid = execute(q["q6_pick"])[0][0]
    out = {}
    for k in sorted((k for k in q if k != "q6_pick"), key=lambda k: int(k[1:])):
        sqls = [s.replace("{sid}", sid) for s in q[k].split(";") if s.strip()]
        ts, n = [], 0
        for _ in range(4):
            t0 = time.perf_counter()
            n = sum(len(execute(s)) for s in sqls)
            ts.append((time.perf_counter() - t0) * 1000)
        out[k] = (ts[0], statistics.median(ts[1:]), n)
        print(k, f"1a={ts[0]:.0f}", f"mediana={out[k][1]:.0f}", f"linhas={n}", sep="\t", flush=True)
    return out


def main():
    kind = sys.argv[1]
    if kind == "duckdb":
        import duckdb
        con = duckdb.connect(sys.argv[2], read_only=True,
                             config={"memory_limit": sys.argv[3] if len(sys.argv) > 3 else "2GB", "threads": "2"})
        ex = lambda s: con.execute(s).fetchall()
        now = ex("SELECT max(time_unix_nano) FROM spans")[0][0]
        print("tamanho", os.path.getsize(sys.argv[2]), "bytes")
        run(ex, qset.queries("duckdb", now))
    else:
        import psycopg
        con = psycopg.connect(DSN, autocommit=True)
        con.execute(f"SET search_path = {sys.argv[2]}")
        ex = lambda s: con.execute(s).fetchall()
        now = ex("SELECT max(time_unix_nano) FROM spans")[0][0]
        for t in ("logs", "spans", "metrics"):
            r = ex(f"""SELECT coalesce(sum(pg_table_size(c.oid)), 0), coalesce(sum(pg_indexes_size(c.oid)), 0)
                       FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
                       WHERE n.nspname = '{sys.argv[2]}' AND c.relkind = 'r' AND (c.relname = '{t}' OR c.relname LIKE '{t}\\_%')""")[0]
            print("tamanho", t, "tabela", r[0], "índices", r[1])
        run(ex, qset.queries("pg", now))


if __name__ == "__main__":
    main()

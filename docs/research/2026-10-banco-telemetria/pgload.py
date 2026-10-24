"""Carrega o DuckDB sintético num Postgres, num de dois layouts, e cria os índices que as consultas pedem.

Uso: pgload.py <arquivo.duckdb> <schema> <plain|part> [tabela]   (com tabela: refaz só ela, no schema existente)
  plain: uma tabela por sinal, PK (dedupe_key), índices B-tree de tempo, conversa e nome.
  part:  a mesma tabela particionada por mês em time_unix_nano (PK passa a (dedupe_key, time_unix_nano): o Postgres
         exige a chave de partição na PK; a dedupe continua valendo porque a chave do agent-studio já inclui o fato).
Tipos: UBIGINT -> bigint (ns cabe até 2262), VARCHAR -> text, JSON -> jsonb, demais iguais.
"""
import os, sys, time
import duckdb, psycopg
sys.path.insert(0, "/workspace/oute-agent/docker/agent-studio")
from agent_studio import store  # noqa: E402

DSN = os.environ.get("PGDSN", "host=127.0.0.1 port=56432 user=bench dbname=tele")
TMP = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data", "csv")
MAP = {"UBIGINT": "bigint", "VARCHAR": "text", "JSON": "jsonb", "TIMESTAMPTZ": "timestamptz", "DOUBLE": "double precision",
       "INTEGER": "integer", "BIGINT": "bigint", "BOOLEAN": "boolean"}
INDEXES = {
    "spans": ["(time_unix_nano)", "(session_id, time_unix_nano)", "(name, time_unix_nano)", "(trace_id, span_id)"],
    "logs": ["(time_unix_nano)", "(session_id, time_unix_nano)", "(event_name, time_unix_nano)"],
    "metrics": ["(time_unix_nano)", "(metric_name, time_unix_nano)"],
}
MONTH_NS = 30 * 86400 * 10**9


def coltype(t):
    base = t.split()[0]
    return MAP[base] + (" NOT NULL" if "NOT NULL" in t else "")


def main(path, schema, layout, only=None):
    duck = duckdb.connect(path, read_only=True, config={"memory_limit": "512MB", "threads": "2"})
    os.makedirs(TMP, exist_ok=True)
    with psycopg.connect(DSN, autocommit=True) as pg:
        if not only:
            pg.execute(f"DROP SCHEMA IF EXISTS {schema} CASCADE; CREATE SCHEMA {schema}")
        for t, cols in store.TABLES.items():
            if only and t != only:
                continue
            if only:
                pg.execute(f"DROP TABLE IF EXISTS {schema}.{t} CASCADE")
            defs = ", ".join(f"{c} {coltype(ty)}" for c, ty in cols)
            if layout == "plain":
                pg.execute(f"CREATE TABLE {schema}.{t} ({defs}, PRIMARY KEY (dedupe_key))")
            else:
                pg.execute(f"CREATE TABLE {schema}.{t} ({defs}, PRIMARY KEY (dedupe_key, time_unix_nano)) "
                           f"PARTITION BY RANGE (time_unix_nano)")
                lo, hi = duck.execute(f"SELECT min(time_unix_nano), max(time_unix_nano) FROM {t}").fetchone()
                start = lo - lo % MONTH_NS
                while start <= hi:
                    pg.execute(f"CREATE TABLE {schema}.{t}_{start // MONTH_NS} PARTITION OF {schema}.{t} "
                               f"FOR VALUES FROM ({start}) TO ({start + MONTH_NS})")
                    start += MONTH_NS
            names = [c for c, _ in cols]
            t0 = time.time()
            n = duck.execute(f"SELECT count(*) FROM {t}").fetchone()[0]
            lo, hi = duck.execute(f"SELECT min(time_unix_nano), max(time_unix_nano) FROM {t}").fetchone()
            step = 30 * 86400 * 10**9
            cur = lo
            while cur <= hi:
                f = os.path.join(TMP, f"{t}.csv")
                duck.execute(f"COPY (SELECT {', '.join(names)} FROM {t} WHERE time_unix_nano >= {cur} AND time_unix_nano < {cur + step}) "
                             f"TO '{f}' (FORMAT csv, HEADER false)")
                pg.execute(f"COPY {schema}.{t} ({', '.join(names)}) FROM '{f}' WITH (FORMAT csv)")
                os.remove(f)
                cur += step
            t1 = time.time()
            for i, ix in enumerate(INDEXES[t]):
                pg.execute(f"CREATE INDEX {t}_ix{i} ON {schema}.{t} {ix}")
            pg.execute(f"ANALYZE {schema}.{t}")
            print(f"{schema}.{t}: {n} linhas, carga {t1 - t0:.0f} s, índices {time.time() - t1:.0f} s", flush=True)


if __name__ == "__main__":
    main(*sys.argv[1:5])

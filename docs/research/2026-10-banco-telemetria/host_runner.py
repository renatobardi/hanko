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

"""Estado derivado (o que o agent-studio guarda no SurrealDB, AS/state.py) em 36 meses: SurrealDB 3.3.0 × Postgres 16.

Volume (do perfil do host, pedido 20261024-181500, e do inventário §3.3), por dia: 180 conversas, 6 rodadas com 3
sessões e 5 etapas cada, 12 pedidos do canal. 36 meses = 1095 dias.
Consultas: s1 as 50 conversas mais novas com o nome da rodada; s2 a página de uma rodada (sessões, etapas, pedidos);
s3 os pedidos pendentes. Tempo: mediana de 5, em ms.
Uso: state.py <porta do surreal em 127.0.0.1> (Postgres no DSN padrão, schema `state`).
"""
import base64, json, os, statistics, sys, time, urllib.request
import psycopg

DAYS, CONV, ROUNDS, SESS, STEPS, REQS = 1095, 180, 6, 3, 5, 12
T0 = 1698105600 * 10**9  # 36 meses antes de 2026-10-24
DAY = 86400 * 10**9
# usuário do servidor de teste local, criado no `surreal start --user … --pass …` da medição
AUTH = base64.b64encode(os.environ.get("SURREAL_TEST_AUTH", "root:root").encode()).decode()


def surreal(url, sql):
    req = urllib.request.Request(url + "/sql", data=sql.encode(), method="POST",
                                 headers={"Accept": "application/json", "surreal-ns": "oute", "surreal-db": "studio",
                                          "Authorization": "Basic " + AUTH})
    with urllib.request.urlopen(req, timeout=600) as r:
        out = json.loads(r.read())
    for o in out:
        if o.get("status") != "OK":
            raise RuntimeError(str(o)[:300])
    return out


def timed(f):
    ts = []
    for _ in range(5):
        t0 = time.perf_counter()
        f()
        ts.append((time.perf_counter() - t0) * 1000)
    return statistics.median(ts)


def main(url):
    surreal(url, "REMOVE DATABASE IF EXISTS studio; DEFINE DATABASE studio;")
    pg = psycopg.connect("host=127.0.0.1 port=56432 user=bench dbname=tele", autocommit=True)
    pg.execute("""DROP SCHEMA IF EXISTS state CASCADE; CREATE SCHEMA state; SET search_path = state;
        CREATE TABLE rodada (id text PRIMARY KEY, name text, opened bigint, closed bigint);
        CREATE TABLE sessao (id text PRIMARY KEY, rodada text REFERENCES rodada, state text, opened bigint);
        CREATE TABLE etapa (rodada text REFERENCES rodada, kind text, key text, verdict text, rev int, PRIMARY KEY (rodada, kind, key));
        CREATE TABLE pedido (id text PRIMARY KEY, rodada text, host text, state text, created bigint);
        CREATE TABLE conversa (id text PRIMARY KEY, rodada text REFERENCES rodada, agent text, host text, phase text, start bigint);
        CREATE INDEX ON conversa (start DESC); CREATE INDEX ON conversa (rodada); CREATE INDEX ON sessao (rodada);
        CREATE INDEX ON pedido (state, created)""")
    t0 = time.time()
    for d in range(DAYS):
        base = T0 + d * DAY
        rs = [f"r{d}_{i}" for i in range(ROUNDS)]
        sq, rows = [], {"rodada": [], "sessao": [], "etapa": [], "pedido": [], "conversa": []}
        for i, r in enumerate(rs):
            rows["rodada"].append((r, f"Name_{d}_{i}", base + i, base + i + 3600 * 10**9))
            sq.append(f"CREATE rodada:{r} SET name = 'Name_{d}_{i}', opened = {base + i}, closed = {base + i + 3600 * 10**9};")
            for s in range(SESS):
                rows["sessao"].append((f"{r}_s{s}", r, "closed", base + s))
                sq.append(f"CREATE sessao:{r}_s{s} SET rodada = rodada:{r}, state = 'closed', opened = {base + s};")
            for e in range(STEPS):
                rows["etapa"].append((r, "triagem" if e == 0 else "merge", f"k{e}", "aprovado", 1))
                sq.append(f"CREATE etapa:[{json.dumps(r)}, 'k{e}'] SET rodada = rodada:{r}, kind = 'merge', verdict = 'aprovado', rev = 1;")
        for p in range(REQS):
            st = "pendente" if (d == DAYS - 1 and p < 3) else "decidido"
            rows["pedido"].append((f"p{d}_{p}", rs[p % ROUNDS], "oute-server", st, base + p))
            sq.append(f"CREATE pedido:p{d}_{p} SET rodada = rodada:{rs[p % ROUNDS]}, host = 'oute-server', state = '{st}', created = {base + p};")
        for c in range(CONV):
            r = rs[c % ROUNDS] if c % 4 == 0 else None
            rows["conversa"].append((f"c{d}_{c}", r, "claude", "oute-server", "build", base + c * 10**9))
            rr = f"rodada:{r}" if r else "NONE"
            sq.append(f"CREATE conversa:c{d}_{c} SET rodada = {rr}, agent = 'claude', host = 'oute-server', phase = 'build', start = {base + c * 10**9};")
        surreal(url, "BEGIN;" + "".join(sq) + "COMMIT;")
        with pg.cursor() as cur:
            for t, vals in rows.items():
                if vals:
                    cur.executemany(f"INSERT INTO state.{t} VALUES ({', '.join(['%s'] * len(vals[0]))})", vals)
    print(f"carga de {DAYS} dias: {time.time() - t0:.0f} s (os dois bancos juntos)")
    surreal(url, "DEFINE INDEX conv_start ON conversa FIELDS start; DEFINE INDEX pedido_state ON pedido FIELDS state;"
                 "DEFINE INDEX sessao_rodada ON sessao FIELDS rodada; DEFINE INDEX etapa_rodada ON etapa FIELDS rodada;")
    r = f"r{DAYS - 1}_0"
    s = {"s1": f"SELECT id, agent, start, rodada.name AS rodada FROM conversa ORDER BY start DESC LIMIT 50;",
         "s2": f"SELECT * FROM rodada:{r}; SELECT * FROM sessao WHERE rodada = rodada:{r}; "
               f"SELECT * FROM etapa WHERE rodada = rodada:{r}; SELECT * FROM pedido WHERE rodada = rodada:{r};",
         "s3": "SELECT * FROM pedido WHERE state = 'pendente';"}
    p = {"s1": "SELECT c.id, c.agent, c.start, r.name FROM state.conversa c LEFT JOIN state.rodada r ON r.id = c.rodada "
               "ORDER BY c.start DESC LIMIT 50",
         "s2": f"SELECT * FROM state.rodada WHERE id = '{r}'; SELECT * FROM state.sessao WHERE rodada = '{r}'; "
               f"SELECT * FROM state.etapa WHERE rodada = '{r}'; SELECT * FROM state.pedido WHERE rodada = '{r}'",
         "s3": "SELECT * FROM state.pedido WHERE state = 'pendente'"}
    pg.execute("ANALYZE state.conversa; ANALYZE state.pedido; ANALYZE state.sessao; ANALYZE state.etapa")
    for k in ("s1", "s2", "s3"):
        ts = timed(lambda: surreal(url, s[k]))
        tp = timed(lambda: [pg.execute(q).fetchall() for q in p[k].split("; ")])
        print(k, f"surreal={ts:.1f}ms", f"postgres={tp:.1f}ms", sep="\t")
    n = pg.execute("SELECT (SELECT count(*) FROM state.conversa), pg_size_pretty(sum(pg_total_relation_size(c.oid))) "
                   "FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'state' AND c.relkind = 'r'").fetchone()
    print("conversas", n[0], "tamanho postgres", n[1])


if __name__ == "__main__":
    main(f"http://127.0.0.1:{int(sys.argv[1])}")  # só o servidor de teste local

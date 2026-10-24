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

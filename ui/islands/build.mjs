// Build das ilhas React (ADR-0005/ADR-0026): esbuild → internal/ui/static/islands/*.js (ESM, minificado, sem sourcemap).
// Determinístico: mesma entrada ⇒ mesmos bytes (o CI roda `git diff --exit-code` depois do build).
// Uso: `node build.mjs` (ou `make islands`). Imprime os tamanhos gzip e falha se estourar o orçamento.
import { build } from "esbuild";
import { readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const here = dirname(fileURLToPath(import.meta.url));
const outdir = resolve(here, "../../internal/ui/static/islands");

// Uma entrada por ilha: o nome do arquivo é o valor de data-island no host. Teto de 3 (ADR-0005; a 4ª exige ADR).
const islands = {
  "run-log": "src/run-log/index.tsx",
  "org-chart": "src/org-chart/index.tsx",
  "anchor-editor": "src/anchor-editor/index.tsx",
};
if (Object.keys(islands).length > 3) throw new Error("ADR-0005: teto de 3 ilhas");

// Orçamento em bytes gzip (ADR-0026): React compartilhado vai para o chunk vendor; cada ilha paga só o seu código.
const budget = { vendor: 75 * 1024, island: 12 * 1024 };

rmSync(outdir, { recursive: true, force: true });
await build({
  absWorkingDir: here,
  entryPoints: islands,
  outdir,
  entryNames: "[name]",
  chunkNames: "vendor-[hash]",
  bundle: true,
  splitting: true,
  format: "esm",
  target: "es2020",
  platform: "browser",
  jsx: "automatic",
  minify: true,
  sourcemap: false,
  legalComments: "none",
  charset: "utf8",
  define: { "process.env.NODE_ENV": '"production"' },
  logLevel: "warning",
});

let failed = false;
for (const f of readdirSync(outdir).sort()) {
  const p = join(outdir, f);
  if (!statSync(p).isFile()) continue;
  const raw = readFileSync(p);
  const gz = gzipSync(raw, { level: 9 }).length;
  const limit = f.startsWith("vendor-") ? budget.vendor : budget.island;
  const over = gz > limit;
  failed ||= over;
  console.log(
    `${f.padEnd(24)} ${String(raw.length).padStart(8)} B  gzip ${String(gz).padStart(7)} B  (orçamento ${limit} B)${over ? "  ESTOUROU" : ""}`,
  );
}
if (failed) {
  console.error("orçamento de tamanho estourado (ver ADR-0026)");
  process.exit(1);
}

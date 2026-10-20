import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { createWriteStream, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { randomBytes } from "node:crypto";
import { RUN_DIR, STATE_FILE, type HarnessState } from "./state";

// Harness do e2e (ADR-0058 §D): zera o schema do Postgres, sobe `bin/hanko serve` (authenticated, provider local,
// adapter fake, sem memória externa) e espera o link de claim do primeiro dono aparecer no stderr do processo.
// A primeira jornada (claim) consome esse link; as demais reaproveitam a sessão dela.

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..", "..");

function fail(msg: string): never {
  throw new Error(`[e2e] ${msg}`);
}

async function waitFor<T>(what: string, timeoutMs: number, probe: () => Promise<T | undefined> | T | undefined): Promise<T> {
  const start = Date.now();
  for (;;) {
    const v = await probe();
    if (v !== undefined) return v;
    if (Date.now() - start > timeoutMs) fail(`tempo esgotado esperando ${what}`);
    await new Promise((r) => setTimeout(r, 150));
  }
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  const bin = process.env.HANKO_BIN ? resolve(process.env.HANKO_BIN) : join(repoRoot, "bin", "hanko");
  if (!existsSync(bin)) fail(`binário não encontrado em ${bin}; rode "make build" na raiz do repositório (ou defina HANKO_BIN)`);

  const dbURL = process.env.HANKO_E2E_DATABASE_URL ?? process.env.HANKO_TEST_DATABASE_URL;
  if (!dbURL) fail("defina HANKO_E2E_DATABASE_URL (ou HANKO_TEST_DATABASE_URL) com um Postgres descartável: o schema public é APAGADO");

  const reset = spawnSync("psql", [dbURL, "-v", "ON_ERROR_STOP=1", "-q", "-c", "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"], { encoding: "utf8" });
  if (reset.status !== 0) fail(`não foi possível zerar o schema (psql): ${reset.stderr || reset.error}`);

  rmSync(RUN_DIR, { recursive: true, force: true });
  mkdirSync(RUN_DIR, { recursive: true });
  const workspaces = mkdtempSync(join(tmpdir(), "hanko-e2e-ws-"));
  const port = Number(process.env.E2E_PORT ?? 17420);
  const baseURL = `http://127.0.0.1:${port}`;
  const logPath = join(RUN_DIR, "hanko.log");
  const log = createWriteStream(logPath);

  const env: NodeJS.ProcessEnv = {
    PATH: process.env.PATH,
    HOME: process.env.HOME,
    HANKO_DATABASE_URL: dbURL,
    HANKO_ADDR: `127.0.0.1:${port}`,
    HANKO_PUBLIC_URL: baseURL,
    HANKO_MODE: "authenticated",
    HANKO_AUTH_PROVIDER: "local",
    HANKO_SECRET: randomBytes(32).toString("hex"),
    HANKO_MEMORY_PROVIDER: "none",
    HANKO_WORKSPACES_ROOT: workspaces,
    HANKO_LOG_LEVEL: "info",
    HANKO_LOG_FORMAT: "json",
    HANKO_HEARTBEAT_INTERVAL: "5s",
    HANKO_WORKERS: "4",
    // sem coletor OTLP no e2e: o exportador falha em silêncio (avisos raros em debug)
    OTEL_EXPORTER_OTLP_ENDPOINT: "http://127.0.0.1:1",
  };
  const child: ChildProcess = spawn(bin, ["serve"], { env, stdio: ["ignore", "pipe", "pipe"] });
  let captured = "";
  const onData = (b: Buffer) => {
    captured += b.toString();
    log.write(b);
  };
  child.stdout?.on("data", onData);
  child.stderr?.on("data", onData);
  let exited: number | null | undefined;
  child.on("exit", (code) => {
    exited = code ?? -1;
  });

  const claimURL = await waitFor("o link de claim no stderr do hanko", 90_000, () => {
    if (exited !== undefined) fail(`hanko encerrou (código ${exited}) antes de ficar pronto:\n${captured.slice(-2000)}`);
    const m = captured.match(/http:\/\/127\.0\.0\.1:\d+\/claim\/[A-Za-z0-9_-]+/);
    return m ? m[0] : undefined;
  });
  await waitFor("/readyz", 30_000, async () => {
    try {
      const r = await fetch(`${baseURL}/readyz`);
      return r.ok ? true : undefined;
    } catch {
      return undefined;
    }
  });

  const state: HarnessState = { baseURL, claimURL, logPath, pid: child.pid ?? 0, workspaces };
  writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));

  return async () => {
    if (exited === undefined) {
      child.kill("SIGTERM");
      await waitFor("o hanko encerrar", 40_000, () => (exited !== undefined ? true : undefined)).catch(() => child.kill("SIGKILL"));
    }
    log.end();
    rmSync(workspaces, { recursive: true, force: true });
  };
}

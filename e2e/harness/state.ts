import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

/** Pasta de trabalho do harness (log do servidor, estado, sessão do dono). Ignorada pelo git. */
export const RUN_DIR = resolve(here, "..", ".run");
export const STATE_FILE = join(RUN_DIR, "state.json");
/** Sessão do dono, gravada pela jornada de claim e reaproveitada pelas demais. */
export const OWNER_STATE = join(RUN_DIR, "owner.json");

export interface HarnessState {
  baseURL: string;
  claimURL: string;
  logPath: string;
  pid: number;
  workspaces: string;
}

export function harnessState(): HarnessState {
  return JSON.parse(readFileSync(STATE_FILE, "utf8")) as HarnessState;
}

/** Empresa criada pela fase de setup; as jornadas trabalham nela. */
export const COMPANY = { name: "Acme E2E", prefix: "ACM", slug: "acme-e2e", base: "/c/acme-e2e" };

/** Credenciais do dono criado pela jornada de claim. */
export const OWNER = { name: "Dona E2E", email: "dona@e2e.hanko.test", password: "senha-e2e-bem-longa-123" };

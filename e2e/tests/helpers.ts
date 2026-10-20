import { expect, type APIResponse, type Page } from "@playwright/test";
import { COMPANY } from "../harness/state";

/** Base da empresa de teste (ex.: /c/acme-e2e). */
export const B = COMPANY.base;
/** Base da API REST da empresa de teste. */
export const API = `/api/v1/companies/${COMPANY.slug}`;

/** Slug que o Hanko dá a um nome (minúsculas, sem acento, hífens). */
export function slugify(name: string): string {
  return name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
}

/** Contrata uma persona com o adapter fake (sem processo, sem rede, sem custo). Devolve o slug. */
export async function hirePersona(page: Page, name: string, opts: { requireReview?: boolean } = {}): Promise<string> {
  await page.goto(`${B}/personas/nova`);
  await page.getByLabel("Nome", { exact: true }).fill(name);
  await page.locator('label:has(input[name="agent"][value="fake"])').click();
  await page.locator("#p-hb").fill("0"); // sem heartbeat periódico: só roda quando é acordada
  if (opts.requireReview) await page.locator('input[name="require_review"]').check({ force: true });
  await page.getByRole("button", { name: "Contratar" }).click();
  await page.waitForURL(new RegExp(`${B}/personas/[a-z0-9-]+$`));
  return slugify(name);
}

/** Cria a persona se ainda não existe. */
export async function ensurePersona(page: Page, name: string): Promise<string> {
  const slug = slugify(name);
  const res = await page.goto(`${B}/personas/${slug}`);
  if (res && res.status() === 404) return hirePersona(page, name);
  return slug;
}

/** Cria uma task pelo formulário, atribuída (ou não) a uma persona. Devolve a chave (ex.: ACM-1). */
export async function createTask(page: Page, title: string, opts: { persona?: string; description?: string } = {}): Promise<string> {
  await page.goto(`${B}/tasks/nova`);
  await page.getByLabel("Título").fill(title);
  if (opts.description) await page.getByLabel("Descrição").fill(opts.description);
  if (opts.persona) await page.locator("#t-persona").selectOption({ label: opts.persona });
  await page.getByRole("button", { name: "Criar task" }).click();
  await page.waitForURL(new RegExp(`${B}/tasks/${COMPANY.prefix}-\\d+`));
  const m = page.url().match(new RegExp(`${COMPANY.prefix}-\\d+`));
  expect(m).not.toBeNull();
  return m![0];
}

/** POST na API com a sessão do navegador (a API exige mesma origem em métodos que mudam estado). */
export async function apiPost(page: Page, path: string, data: unknown): Promise<APIResponse> {
  const origin = new URL(page.url() === "about:blank" ? (process.env.E2E_BASE ?? "http://127.0.0.1:17420") : page.url()).origin;
  return page.request.post(path, { data, headers: { Origin: origin } });
}

/** Recarrega até a condição valer (as runs do fake terminam em segundos, mas as telas não são ao vivo). */
export async function untilReload(page: Page, check: () => Promise<void>, timeoutMs = 45_000): Promise<void> {
  await expect(async () => {
    await page.reload();
    await check();
  }).toPass({ timeout: timeoutMs, intervals: [1_000, 2_000] });
}

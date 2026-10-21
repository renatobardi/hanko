import { request } from "@playwright/test";
import { expect, test } from "./fixtures";
import { COMPANY, harnessState } from "../harness/state";
import { B } from "./helpers";
import { createApiKey } from "./keys";

const LABEL = "Escritas por minuto (por API key ou usuário)";

// Jornada 14 — rate limit (#51): o dono baixa o teto de escritas em Configurações › Instância; uma API key que
// estoura leva 429 com Retry-After (problem+json), volta a passar depois da espera, a métrica conta a decisão, e
// "Voltar ao padrão" restaura o limite.
test("rate limit: 429 com Retry-After, métrica e volta ao padrão", async ({ page }) => {
  const key = await createApiKey(page, "e2e-ratelimit");

  await page.goto(`${B}/configuracoes/instancia`);
  const input = page.getByLabel(LABEL);
  await input.fill("10");
  await input.locator("xpath=ancestor::form").getByRole("button", { name: "Salvar" }).click();
  await expect(page.getByLabel(LABEL)).toHaveValue("10");

  const api = await request.newContext({ baseURL: harnessState().baseURL, extraHTTPHeaders: { Authorization: `Bearer ${key}` } });
  try {
    // escrita inválida de propósito (422): conta no bucket e não cria nada
    const statuses: number[] = [];
    let limited: Awaited<ReturnType<typeof api.post>> | undefined;
    for (let i = 0; i < 14; i++) {
      const res = await api.post(`/api/v1/companies/${COMPANY.slug}/tasks`, { data: {} });
      statuses.push(res.status());
      if (res.status() === 429 && !limited) limited = res;
    }
    expect(statuses.slice(0, 10).every((s) => s !== 429), `as 10 primeiras passam: ${statuses}`).toBe(true);
    expect(limited, `alguma resposta deveria ser 429: ${statuses}`).toBeDefined();
    const retry = Number(limited!.headers()["retry-after"]);
    expect(retry).toBeGreaterThanOrEqual(1);
    expect(retry).toBeLessThanOrEqual(7); // 10/min recarrega 1 token a cada 6 s
    expect(limited!.headers()["ratelimit-remaining"]).toBe("0");
    expect(limited!.headers()["content-type"]).toContain("application/problem+json");
    expect(((await limited!.json()) as { status: number }).status).toBe(429);

    // leituras não consomem: a API segue lendo enquanto as escritas estão barradas
    const read = await api.get(`/api/v1/companies/${COMPANY.slug}/tasks`);
    expect(read.status()).toBe(200);

    // depois do Retry-After a escrita volta a passar
    await expect(async () => {
      const res = await api.post(`/api/v1/companies/${COMPANY.slug}/tasks`, { data: {} });
      expect(res.status()).not.toBe(429);
    }).toPass({ timeout: 15_000, intervals: [retry * 1000 + 300] });

    // a métrica OTel/Prometheus registrou a decisão
    const metrics = await (await api.get("/metrics")).text();
    expect(metrics).toMatch(/hanko_ratelimit_decisions_total\{[^}]*class="write"[^}]*outcome="limited"[^}]*\}\s+[1-9]/);
  } finally {
    await api.dispose();
  }

  // restaura o padrão pela tela
  await page.goto(`${B}/configuracoes/instancia`);
  await page.getByLabel(LABEL).locator("xpath=ancestor::tr").getByRole("button", { name: "Voltar ao padrão" }).click();
  await expect(page.getByLabel(LABEL)).toHaveValue("600");
});

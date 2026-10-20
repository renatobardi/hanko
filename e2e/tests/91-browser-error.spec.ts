import { expect, request, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { harnessState } from "../harness/state";
import { B } from "./helpers";
import { createApiKey } from "./keys";

// Jornada 15 — erros do navegador → Hanko → OTLP (#49): um erro de JavaScript na página vira span/log/contador no
// servidor, com a pilha e sem a query da URL.
test("erro do navegador: hanko.js relata e o servidor registra a exceção", async ({ page }) => {
  const key = await createApiKey(page, "e2e-erros");
  const { logPath, baseURL } = harnessState();
  const marker = `e2e-boom-${Date.now()}`;

  await page.goto(`${B}/tasks?q=segredo-na-query#frag`);
  await page.evaluate((m) => {
    setTimeout(() => {
      function falha() {
        throw new TypeError(m);
      }
      falha();
    }, 0);
  }, marker);

  // o log estruturado tem a exceção do navegador, com tipo, mensagem e pilha
  await expect
    .poll(
      () =>
        readFileSync(logPath, "utf8")
          .split("\n")
          .filter((l) => l.includes(marker) && l.includes('"exception.source":"browser"')),
      { timeout: 20_000 },
    )
    .toHaveLength(1);
  const line = JSON.parse(
    readFileSync(logPath, "utf8")
      .split("\n")
      .find((l) => l.includes(marker) && l.includes('"exception.source":"browser"'))!,
  ) as Record<string, string>;
  expect(line.level).toBe("ERROR");
  expect(line["exception.type"]).toBe("TypeError");
  expect(line["exception.message"]).toBe(marker);
  expect(line["exception.stacktrace"]).toContain("falha");
  expect(line["url.path"]).toBe(`${B}/tasks`);
  expect(JSON.stringify(line)).not.toContain("segredo-na-query"); // a query da URL nunca vai para a telemetria

  // e o contador Prometheus enxerga a origem browser
  const api = await request.newContext({ baseURL, extraHTTPHeaders: { Authorization: `Bearer ${key}` } });
  try {
    const metrics = await (await api.get("/metrics")).text();
    expect(metrics).toMatch(/hanko_errors_total\{[^}]*source="browser"[^}]*\}\s+[1-9]/);
  } finally {
    await api.dispose();
  }
});

// O endpoint só aceita a mesma origem e exige sessão: de fora não registra nada.
test("erro do navegador: o endpoint recusa quem não tem sessão", async () => {
  const { baseURL } = harnessState();
  const anon = await request.newContext({ baseURL });
  try {
    const res = await anon.post("/telemetry/errors", { data: { message: "x" }, maxRedirects: 0 });
    expect([302, 303, 401, 403]).toContain(res.status());
  } finally {
    await anon.dispose();
  }
});

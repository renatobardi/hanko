import { expect, test } from "./fixtures";

// Jornada 17 — ux-labs (#70): cenários montados com dados sintéticos, sem tocar o banco.
test("ux-labs: thread com 500 comentários renderiza inteiro", async ({ page }) => {
  await page.goto("/dev/ux");
  await expect(page.getByRole("link", { name: /Thread longo/ })).toBeVisible();
  await page.getByRole("link", { name: /Thread longo/ }).click();
  await expect(page.getByTestId("ux-thread-count")).toContainText("500");
  await expect(page.locator("#thread .msg")).toHaveCount(500);
  // rola até o fim sem travar e o último comentário existe
  await page.locator("#thread .msg").last().scrollIntoViewIfNeeded();
  await expect(page.locator("#thread .msg").last()).toContainText("#500");
});

test("ux-labs: run com 10 mil linhas usa o log virtualizado e pagina para trás", async ({ page }) => {
  await page.goto("/dev/ux/run-10k");
  await expect(page.getByTestId("ux-run-total")).toContainText("10000");
  const host = page.locator('[data-island="run-log"]');
  await expect(host).toHaveAttribute("data-island-state", "ready", { timeout: 20_000 });

  // a ilha mostra o fim do log (o evento 10000) e renderiza só uma janela de linhas, não as 10 mil
  await expect(host).toContainText("10000", { timeout: 15_000 });
  const rendered = await host.locator(".log-line, [data-seq]").count();
  expect(rendered).toBeGreaterThan(0);
  expect(rendered).toBeLessThan(400);

  // rolando para o começo, carrega páginas anteriores da API sintética
  const scroller = host.locator("[role=log], .log").first();
  await scroller.evaluate((el) => (el.scrollTop = 0));
  await expect(host).toContainText(/Carregando|eventos/);
  await expect
    .poll(async () => (await host.innerText()).match(/(\d[\d.]*)\s+eventos/)?.[1]?.replace(".", "") ?? "0", { timeout: 15_000 })
    .not.toBe("0");
});

test("ux-labs: a API sintética pagina tail, before e after", async ({ request }) => {
  const tail = await (await request.get("/dev/ux/api/runs/run-10k/events?tail=true&limit=5")).json();
  expect(tail.items.map((e: { seq: number }) => e.seq)).toEqual([9996, 9997, 9998, 9999, 10000]);
  const before = await (await request.get("/dev/ux/api/runs/run-10k/events?before=9996&limit=3")).json();
  expect(before.items.map((e: { seq: number }) => e.seq)).toEqual([9993, 9994, 9995]);
});

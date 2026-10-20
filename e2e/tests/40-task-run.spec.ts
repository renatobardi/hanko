import { expect, test } from "./fixtures";
import { B, createTask, ensurePersona, untilReload } from "./helpers";

// Jornada 6 — a task atribuída acorda a persona, o adapter fake roda e a task termina com o resultado nos comentários.
test("task: atribuída à Ada roda com o fake, abre a execução e mostra o log", async ({ page }) => {
  await ensurePersona(page, "Ada");
  const key = await createTask(page, "Escrever o plano da semana", { persona: "Ada", description: "Resumir as metas e os riscos." });

  // a persona é acordada na hora; o fake responde em poucos segundos (a tela não é ao vivo: recarrega)
  await untilReload(page, async () => {
    await expect(page.locator("#thread")).toContainText(`Fake concluiu ${key}`, { timeout: 1_500 });
  });

  // a execução tem página e log com os eventos do fake
  await page.getByRole("link", { name: "execução" }).first().click();
  await page.waitForURL(new RegExp(`${B}/execucoes/`));
  await expect(page.locator(".log-host")).toContainText("sessão iniciada", { timeout: 15_000 });
  await expect(page.locator(".log-host")).toContainText("hanko_task_get");

  // e aparece na lista de execuções
  await page.goto(`${B}/execucoes`);
  await expect(page.getByText(key).first()).toBeVisible();
});

// Jornada 7 — comentar na task: o comentário entra no thread, com markdown.
test("task: comentário humano entra no thread", async ({ page }) => {
  await ensurePersona(page, "Ada");
  const key = await createTask(page, "Revisar o orçamento");
  await page.getByPlaceholder(/Escreva um comentário/).fill("Priorize os custos de **infra** primeiro.");
  await page.getByRole("button", { name: /Comentar|Enviar/ }).click();
  await expect(page.locator("#thread")).toContainText("Priorize os custos de");
  await expect(page.locator("#thread strong")).toHaveText("infra");
  await page.reload();
  await expect(page.locator("#thread")).toContainText("Priorize os custos de");
  await expect(page.getByText(key).first()).toBeVisible();
});

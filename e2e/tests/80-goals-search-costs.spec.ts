import { expect, test } from "./fixtures";
import { B } from "./helpers";

// Jornada 11 — metas: criar uma meta numérica e registrar progresso.
test("meta: criar e registrar progresso", async ({ page }) => {
  await page.goto(`${B}/metas/nova`);
  await page.getByLabel("Título").fill("Chegar a 100 usuários ativos");
  await page.getByLabel("O que se mede").fill("usuários ativos");
  await page.getByLabel("Valor inicial").fill("10");
  await page.getByLabel("Alvo").fill("100");
  await page.getByRole("button", { name: "Criar meta" }).click();
  await page.waitForURL(new RegExp(`${B}/metas/[a-z0-9-]+$`));
  await expect(page.getByRole("heading", { name: /Chegar a 100 usuários ativos/ })).toBeVisible();

  await page.getByLabel("Valor atual").fill("55");
  await page.getByLabel("Nota (opcional)").fill("Fechamos o piloto");
  await page.getByRole("button", { name: "Registrar progresso" }).click();
  await expect(page.getByText("Fechamos o piloto").first()).toBeVisible();
  await expect(page.getByText(/50\s?%|55/).first()).toBeVisible();
});

// Jornada 12 — busca única: acha a task criada por outra jornada.
test("busca: acha tasks e personas pelo texto", async ({ page }) => {
  await page.goto(`${B}/buscar`);
  await page.locator("#q-search").fill("plano da semana");
  await page.locator("#q-search").press("Enter");
  await expect(page.locator("#search-results")).toContainText("Escrever o plano da semana");
  await page.locator("#q-search").fill("Ada");
  await page.locator("#q-search").press("Enter");
  await expect(page.locator("#search-results")).toContainText("Ada");
});

// Jornada 13 — custos: a tela abre e o limite mensal da empresa é salvo.
test("custos: definir o limite mensal da empresa", async ({ page }) => {
  await page.goto(`${B}/custos`);
  await expect(page.getByRole("heading", { name: /Custos/ }).first()).toBeVisible();
  const budget = page.locator('input[name="budget"]').first();
  await budget.fill("50");
  await page.getByRole("button", { name: "Salvar limite" }).first().click();
  await expect(page.locator('input[name="budget"]').first()).toHaveValue(/^50([.,]00)?$/);
});

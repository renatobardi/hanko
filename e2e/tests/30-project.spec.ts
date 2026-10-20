import { expect, test } from "./fixtures";
import { B } from "./helpers";

// Jornada 5 — criar um projeto e vê-lo na lista e na página própria.
test("projeto: criar e abrir", async ({ page }) => {
  await page.goto(`${B}/projetos/novo`);
  await page.getByLabel("Nome", { exact: true }).fill("Lançamento v1");
  await page.getByLabel("Descrição").fill("Primeiro lançamento público");
  await page.getByRole("button", { name: /Criar projeto|Salvar/ }).click();
  await page.waitForURL(new RegExp(`${B}/projetos/[a-z0-9-]+$`));
  await expect(page.getByRole("heading", { name: /Lançamento v1/ })).toBeVisible();

  await page.goto(`${B}/projetos`);
  await expect(page.getByRole("link", { name: /Lançamento v1/ }).first()).toBeVisible();
});

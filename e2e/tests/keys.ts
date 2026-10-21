import { expect, type Page } from "@playwright/test";
import { B } from "./helpers";

/** Cria uma API key pela tela de Configurações e devolve o valor (mostrado uma única vez). */
export async function createApiKey(page: Page, name: string): Promise<string> {
  await page.goto(`${B}/configuracoes`);
  await page.getByLabel("Nome da chave").fill(name);
  await page.getByRole("button", { name: "Criar chave" }).click();
  const box = page.locator(".keybox").first();
  await expect(box).toContainText("hk_");
  return (await box.innerText()).trim();
}

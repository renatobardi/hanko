import { expect, test } from "./fixtures";
import { COMPANY, OWNER } from "../harness/state";

// Jornada 3 — entrar e sair: senha errada é recusada, a certa abre a sessão e "Sair" a encerra.
test.use({ storageState: { cookies: [], origins: [] } }); // contexto limpo: sem a sessão gravada pelo setup

test("login: senha errada é recusada, a certa entra e sair encerra a sessão", async ({ page }) => {
  await page.goto("/entrar");
  await expect(page.getByRole("heading", { name: "Entrar" })).toBeVisible();

  await page.getByLabel("E-mail").fill(OWNER.email);
  await page.getByLabel("Senha").fill("senha-errada-123");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByText("E-mail ou senha incorretos.")).toBeVisible();

  await page.getByLabel("Senha").fill(OWNER.password);
  await page.getByRole("button", { name: "Entrar" }).click();
  await page.waitForURL(`**${COMPANY.base}/**`);

  // rota protegida sem sessão volta para o login; com sessão abre
  await page.goto(`${COMPANY.base}/tasks`);
  await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

  await page.locator("details.usermenu > summary").click();
  await page.locator('form[action="/sair"] button[type="submit"]').click();
  await page.waitForURL("**/entrar");
  await page.goto(`${COMPANY.base}/tasks`);
  await page.waitForURL(/\/entrar/);
});

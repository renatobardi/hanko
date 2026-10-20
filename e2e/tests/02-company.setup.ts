import { expect, test } from "@playwright/test";
import { COMPANY, OWNER_STATE } from "../harness/state";

test.use({ storageState: OWNER_STATE }); // a sessão que a jornada de claim acabou de gravar

// Jornada 2 — a pessoa recém-chegada cria a empresa no onboarding e cai no painel dela.
test("empresa: onboarding cria a empresa e abre o painel", async ({ page, context }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Criar empresa" })).toBeVisible();

  await page.getByLabel("Nome da empresa").fill(COMPANY.name);
  await page.getByLabel("Prefixo das tasks").fill(COMPANY.prefix);
  await page.getByRole("button", { name: "Criar empresa e continuar" }).click();

  await page.waitForURL(`**${COMPANY.base}/**`);
  await expect(page.locator(".crumbs .cur, .crumbs a").first()).toContainText(COMPANY.name);
  await expect(page.getByRole("link", { name: "Tasks" }).first()).toBeVisible();
  await context.storageState({ path: OWNER_STATE });
});

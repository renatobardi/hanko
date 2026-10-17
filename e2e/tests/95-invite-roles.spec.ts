import { expect, test } from "./fixtures";
import { B } from "./helpers";

// Jornada 19 — convite e papéis: o dono convida um membro; o convidado cria a conta pelo link de uso único e só vê o
// que o papel dele permite (as rotas de desenvolvimento e a tela da instância são do dono/admin).
test("convite: o membro entra pelo link e não alcança as telas de dono", async ({ page, browser }) => {
  await page.goto(`${B}/configuracoes`);
  await page.getByLabel("E-mail da pessoa").fill("membro@e2e.hanko.test");
  await page.getByRole("button", { name: "Convidar" }).click();
  const link = (await page.locator("#invite-link").innerText()).trim();
  expect(link).toMatch(/\/convite\/[A-Za-z0-9_-]+$/);

  // o convidado, em outro navegador (sem a sessão do dono)
  const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] }, locale: "pt-BR" });
  const guest = await ctx.newPage();
  await guest.goto(link);
  await guest.getByLabel("Nome").fill("Membro E2E");
  await guest.getByLabel("Senha").fill("senha-do-membro-123");
  await guest.getByRole("button", { name: "Criar conta e aceitar" }).click();
  await guest.waitForURL(`**${B}/**`);
  await expect(guest.getByRole("heading", { name: /Painel|Dashboard|Acme/ }).first()).toBeVisible();

  // o link é de uso único
  const reuse = await guest.goto(link);
  expect([404, 200, 303]).toContain(reuse?.status());
  await expect(guest.getByText(/inválido|expirado|já/i).first()).toBeVisible();

  // telas de dev e da instância: 404 para o membro, 200 para o dono
  for (const path of ["/design-guide", "/dev/ux", "/dev/ux/thread-longo"]) {
    const asGuest = await guest.goto(path);
    expect(asGuest?.status(), `${path} para o membro`).toBe(404);
    const asOwner = await page.goto(path);
    expect(asOwner?.status(), `${path} para o dono`).toBe(200);
  }
  await guest.goto(`${B}/configuracoes/instancia`);
  await expect(guest.getByText("Só o dono da instalação.")).toBeVisible();
  await expect(guest.getByLabel("Escritas por minuto (por API key ou usuário)")).toHaveCount(0);
  await ctx.close();
});

import { expect, test } from "@playwright/test";
import { OWNER, OWNER_STATE, harnessState } from "../harness/state";

// Jornada 1 — first-admin claim (#53): o boot imprime o link de uso único; o primeiro dono entra por ele e o link
// se invalida. A sessão do dono é gravada para as demais jornadas.
test("claim: o link do boot cria o primeiro dono e se invalida", async ({ page, context, request }) => {
  const { claimURL, baseURL } = harnessState();

  // sem nenhum usuário a tela de entrada ainda não tem login (em loopback ela oferece o formulário antigo de dono)
  await page.goto("/entrar");
  await expect(page.getByRole("heading", { name: "Criar conta de dono" })).toBeVisible();

  // um link adulterado responde o mesmo "inválido" de um link usado
  const bad = await page.goto(claimURL.replace(/.$/, (c) => (c === "A" ? "B" : "A")));
  expect(bad?.status()).toBe(404);
  await expect(page.getByTestId("claim-invalid")).toBeVisible();

  const res = await page.goto(claimURL);
  expect(res?.status()).toBe(200);
  expect(res?.headers()["referrer-policy"]).toBe("no-referrer");
  await expect(page.getByTestId("claim-form")).toBeVisible();

  // senha curta não queima o link
  await page.getByLabel("Nome").fill(OWNER.name);
  await page.getByLabel("E-mail").fill(OWNER.email);
  await page.getByLabel("Senha").fill("curta");
  await page.getByLabel("Senha").evaluate((el: HTMLInputElement) => el.removeAttribute("minlength"));
  await page.getByRole("button", { name: "Criar conta e entrar" }).click();
  await expect(page.getByText("password:")).toBeVisible();
  await expect(page.getByTestId("claim-form")).toBeVisible();

  await page.getByLabel("Senha").fill(OWNER.password);
  await page.getByRole("button", { name: "Criar conta e entrar" }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/claim/"));
  // dono logado: sem empresa ainda, cai no onboarding
  await expect(page.getByRole("heading", { name: "Criar empresa" })).toBeVisible();

  // o link não vale mais, nem para quem tem sessão
  const again = await request.get(claimURL);
  expect(again.status()).toBe(404);
  expect(new URL(claimURL).origin).toBe(baseURL);

  await context.storageState({ path: OWNER_STATE });
});

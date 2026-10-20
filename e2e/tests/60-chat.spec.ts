import { expect, test } from "./fixtures";
import { B, ensurePersona, untilReload } from "./helpers";

// Jornada 9 — conversa direta com uma persona: a primeira mensagem abre a conversa, o fake responde, e uma segunda
// mensagem retoma a sessão.
test("chat: conversar com a Ada e continuar a conversa", async ({ page }) => {
  await ensurePersona(page, "Ada");
  await page.goto(`${B}/chat`);
  const ada = await page.locator("#c-persona option", { hasText: "Ada" }).first().getAttribute("value");
  await page.locator("#c-persona").selectOption(ada!);
  await page.getByLabel("Primeira mensagem").fill("Oi Ada, o que você consegue fazer por mim?");
  await page.getByRole("button", { name: "Conversar" }).click();
  await page.waitForURL(new RegExp(`${B}/chat/[0-9a-f-]+$`));
  await expect(page.locator("#chat-thread")).toContainText("Oi Ada, o que você consegue fazer por mim?");

  await untilReload(page, async () => {
    await expect(page.locator("#chat-thread")).toContainText("Fake respondeu na conversa", { timeout: 1_500 });
  });

  await page.getByPlaceholder(/Escreva uma mensagem/).fill("Obrigada! Continue.");
  await page.getByRole("button", { name: /Enviar/ }).click();
  await expect(page.locator("#chat-thread")).toContainText("Obrigada! Continue.");
  await untilReload(page, async () => {
    await expect(page.locator("#chat-thread")).toContainText("sessão retomada", { timeout: 1_500 });
  });
});

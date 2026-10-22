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

// Regressão: o autosave do rascunho (1 s depois da última tecla) disparava a limpeza do compositor, que apagava o
// texto e o arquivo escolhido. Agora só o envio limpa, e o anexo chega junto da mensagem.
test("chat: o rascunho salvo não apaga o texto nem o anexo; o envio leva os dois", async ({ page }) => {
  await ensurePersona(page, "Ada");
  await page.goto(`${B}/chat`);
  const ada = await page.locator("#c-persona option", { hasText: "Ada" }).first().getAttribute("value");
  await page.locator("#c-persona").selectOption(ada!);
  await page.getByLabel("Primeira mensagem").fill("Conversa do teste de rascunho");
  await page.getByRole("button", { name: "Conversar" }).click();
  await page.waitForURL(new RegExp(`${B}/chat/[0-9a-f-]+$`));

  const box = page.getByPlaceholder(/Escreva uma mensagem/);
  await page.locator("form[data-composer] input[type=file]").setInputFiles({
    name: "notas.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("conteúdo do anexo"),
  });
  await box.click();
  await box.pressSequentially("texto que não pode sumir");
  await expect(page.locator("form[data-composer] .draft-state")).toContainText("Rascunho salvo");
  await expect(box).toHaveValue("texto que não pode sumir");
  await expect(page.locator("form[data-composer] [data-dz-list]")).toContainText("notas.txt");

  await page.locator("form[data-composer]").getByRole("button", { name: /Enviar/ }).click();
  await expect(page.locator("#chat-thread")).toContainText("texto que não pode sumir");
  await expect(page.locator("#chat-thread .att-inline")).toContainText("notas.txt");
  await expect(box).toHaveValue("");

  // o rascunho foi apagado pelo envio e não volta ao recarregar
  await page.reload();
  await expect(page.getByPlaceholder(/Escreva uma mensagem/)).toHaveValue("");
});

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
  const consoleErrors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
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
  await expect(page.locator("form[data-composer] [data-dz-list] .att-thumb")).toHaveAttribute("title", /notas\.txt/);

  await page.locator("form[data-composer]").getByRole("button", { name: /Enviar/ }).click();
  await expect(page.locator("#chat-thread")).toContainText("texto que não pode sumir");
  await expect(page.locator('#chat-thread .att-inline .att-thumb[title^="notas.txt"]')).toBeVisible();
  await expect(box).toHaveValue("");

  // o rascunho foi apagado pelo envio e não volta ao recarregar
  await page.reload();
  await expect(page.getByPlaceholder(/Escreva uma mensagem/)).toHaveValue("");
  // o autosave não herda o hx-disabled-elt do formulário (o htmx reclamaria a cada tecla)
  expect(consoleErrors.filter((t) => t.includes("hx-disabled-elt"))).toEqual([]);
});

const PNG_1X1 =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";

// Compositor: colar imagem vira miniatura (com nome ao passar o mouse); Enter envia e Shift+Enter quebra a linha; o
// alfinete solta a caixa e a escolha fica; as setas flutuantes levam ao início e ao fim.
test("chat: colar, Enter, alfinete e setas", async ({ page }) => {
  await ensurePersona(page, "Ada");
  await page.goto(`${B}/chat`);
  const ada = await page.locator("#c-persona option", { hasText: "Ada" }).first().getAttribute("value");
  await page.locator("#c-persona").selectOption(ada!);
  await page.getByLabel("Primeira mensagem").fill("Conversa do teste do compositor");
  await page.getByLabel("Primeira mensagem").press("Enter"); // Enter também cria a conversa
  await page.waitForURL(new RegExp(`${B}/chat/[0-9a-f-]+$`));

  const form = page.locator("form[data-composer]");
  const box = page.getByPlaceholder(/Escreva uma mensagem/);

  // colar uma imagem da área de transferência: miniatura com o nome legível no title e botão de remover
  await box.click();
  await box.evaluate((el, b64) => {
    const bin = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bin], "image.png", { type: "image/png" }));
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, PNG_1X1);
  const thumb = form.locator("[data-dz-list] .att-thumb");
  await expect(thumb).toHaveCount(1);
  await expect(thumb).toHaveAttribute("title", /^colado-\d{4}-\d{2}-\d{2}-\d{4}\.png · /);
  await expect(thumb.locator("canvas")).toHaveCount(1); // a miniatura do que foi colado é desenhada num canvas
  await thumb.locator(".dz-remove").click();
  await expect(thumb).toHaveCount(0);

  // Shift+Enter quebra a linha e não envia
  await box.fill("linha 1");
  await box.press("Shift+Enter");
  await box.pressSequentially("linha 2");
  await expect(box).toHaveValue("linha 1\nlinha 2");
  await expect(page.locator("#chat-thread")).not.toContainText("linha 2");

  // colar de novo e Enter: envia o texto com a miniatura na mensagem
  await box.evaluate((el, b64) => {
    const bin = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bin], "image.png", { type: "image/png" }));
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  }, PNG_1X1);
  await expect(thumb).toHaveCount(1);
  await box.press("Enter");
  await expect(page.locator("#chat-thread")).toContainText("linha 2");
  await expect(page.locator("#chat-thread .att-inline .att-thumb img")).toHaveCount(1);
  await expect(box).toHaveValue("");
  await expect(thumb).toHaveCount(0);
  // Enter com a caixa vazia e sem anexo não envia nada
  const bubbles = await page.locator("#chat-thread .msg.me").count();
  await box.press("Enter");
  await page.waitForTimeout(400);
  await expect(page.locator("#chat-thread .msg.me")).toHaveCount(bubbles);

  // alfinete: fixo por padrão; desligar solta a caixa e a escolha sobrevive ao recarregar
  const pin = form.locator("[data-composer-pin]");
  await expect(form).toHaveClass(/is-pinned/);
  await expect(pin).toHaveAttribute("aria-pressed", "true");
  await pin.click();
  await expect(form).not.toHaveClass(/is-pinned/);
  await page.reload();
  await expect(page.locator("form[data-composer]")).not.toHaveClass(/is-pinned/);
  await page.locator("form[data-composer] [data-composer-pin]").click();
  await expect(page.locator("form[data-composer]")).toHaveClass(/is-pinned/);

  // setas flutuantes: com a página alta, descem ao fim e sobem ao início
  await page.evaluate(() => {
    const spacer = document.createElement("div");
    spacer.style.height = "4000px";
    document.getElementById("chat-thread")!.before(spacer);
    window.scrollTo(0, 0);
    window.dispatchEvent(new Event("scroll"));
  });
  const down = page.locator('[data-scroll-to="bottom"]');
  const up = page.locator('[data-scroll-to="top"]');
  await expect(down).toHaveClass(/is-on/);
  await expect(up).not.toHaveClass(/is-on/);
  await down.click();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollHeight - (window.scrollY + window.innerHeight))).toBeLessThan(5);
  await expect(up).toHaveClass(/is-on/);
  await up.click();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
});

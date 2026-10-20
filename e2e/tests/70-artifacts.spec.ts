import { expect, test } from "./fixtures";
import { B } from "./helpers";

// Jornada 10 — artefatos: subir um arquivo, vê-lo na lista e na página própria; o mesmo nome cria a versão seguinte.
test("artefato: enviar, listar e versionar", async ({ page }) => {
  const send = async (body: string) => {
    await page.goto(`${B}/artefatos/novo`);
    await page.setInputFiles("#a-file", { name: "plano-e2e.md", mimeType: "text/markdown", buffer: Buffer.from(body) });
    await page.getByRole("button", { name: "Enviar" }).click();
    await page.waitForURL(new RegExp(`${B}/artefatos/[0-9a-f-]+`));
  };

  await send("# Plano e2e\n\n- primeiro item\n");
  await expect(page.getByRole("heading", { name: /plano-e2e\.md/ })).toBeVisible();
  await expect(page.getByText("v1").first()).toBeVisible();

  await page.goto(`${B}/artefatos`);
  await expect(page.getByRole("link", { name: /plano-e2e\.md/ }).first()).toBeVisible();

  await send("# Plano e2e\n\n- primeiro item\n- segundo item\n");
  await expect(page.getByText("v2").first()).toBeVisible();
});

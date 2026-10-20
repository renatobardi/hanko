import { expect, test } from "./fixtures";
import { API, B, apiPost, createTask, untilReload } from "./helpers";

// Jornada 8 — gate: a persona (fake, que chama a tool MCP hanko_gate_request como um agente de verdade) pede uma
// decisão; o humano a vê em Gates e a aprova com uma nota; a decisão fica registrada.
test("gate: a persona pede, o humano aprova com nota", async ({ page }) => {
  await page.goto(`${B}/`);
  const hired = await apiPost(page, `${API}/personas`, {
    name: "Bento",
    agent: "fake",
    model: "fake",
    heartbeat_interval_sec: 0,
    adapter_config: { gate: { title: "Publicar o relatório semanal?", body: "Vai para a lista de diretoria. Impacto: **baixo**." } },
  });
  expect(hired.status(), await hired.text()).toBeLessThan(300);

  await createTask(page, "Publicar o relatório semanal", { persona: "Bento" });

  // a run do fake chama o MCP do Hanko e o gate aparece na lista
  await page.goto(`${B}/gates`);
  await untilReload(page, async () => {
    await expect(page.getByText("Publicar o relatório semanal?").first()).toBeVisible({ timeout: 1_500 });
  });
  await page.getByRole("link", { name: /Publicar o relatório semanal\?/ }).first().click();
  await page.waitForURL(new RegExp(`${B}/gates/`));
  await expect(page.locator(".md strong").first()).toHaveText("baixo");

  await page.getByLabel("Nota da decisão").fill("Aprovado: pode publicar.");
  await page.getByRole("button", { name: /Aprovar/ }).click();
  await expect(page.getByText("Aprovado: pode publicar.").first()).toBeVisible();

  // a decisão fica na lista de decisões
  await page.goto(`${B}/decisoes`);
  await expect(page.getByText("Publicar o relatório semanal?").first()).toBeVisible();
});

import { expect, test } from "./fixtures";
import { B } from "./helpers";

// Jornada 18 — preferências: idioma (pt-BR/en) e tema (claro/escuro) trocam pela tela e sobrevivem ao recarregar.
test("idioma e tema: trocar e persistir", async ({ page }) => {
  await page.goto(`${B}/tasks`);
  await expect(page.locator("html")).toHaveAttribute("lang", "pt-BR");
  await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

  // inglês
  await page.locator('.lang-switch button[value="en"]').click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("link", { name: "Personas" }).first()).toBeVisible();
  await expect(page.getByText("In progress").first()).toBeVisible();
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");

  // tema escuro
  await page.locator('.theme-switch button[value="dark"]').click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  const lum = await page.evaluate(() => {
    const v = getComputedStyle(document.documentElement).getPropertyValue("--background").trim(); // oklch(L C H)
    const m = v.match(/oklch\(\s*([\d.]+)(%?)/);
    return m ? Number(m[1]) / (m[2] ? 100 : 1) : NaN;
  });
  expect(lum).toBeLessThan(0.3);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  // volta ao padrão (pt-BR, sistema) para não contaminar as demais jornadas
  await page.locator('.lang-switch button[value="pt-BR"]').click();
  await page.locator('.theme-switch button[value="system"]').click();
  await expect(page.locator("html")).toHaveAttribute("lang", "pt-BR");
});

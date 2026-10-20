import { type Page } from "@playwright/test";
import { expect, test } from "./fixtures";

// Jornada 16 — /design-guide (#70) e screenshot diff do DS (#57): o catálogo do Kubo DS em claro e escuro é a base
// visual. As baselines ficam em tests/__screenshots__ (Linux/Chromium; a fonte Inter é servida localmente).
// Para regerar depois de uma mudança intencional no DS: `npx playwright test --update-snapshots`.

async function settle(page: Page) {
  await page.evaluate(() => document.fonts.ready);
  await page.addStyleTag({ content: "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}" });
}

test("design-guide: tokens e componentes em claro e escuro, lado a lado", async ({ page }) => {
  await page.goto("/design-guide");
  await settle(page);
  await expect(page.getByRole("heading", { name: "Kubo DS · catálogo vivo" })).toBeVisible();

  const light = page.locator('[data-pane="light"]');
  const dark = page.locator('[data-pane="dark"]');
  for (const s of ["cores", "tipografia", "botoes", "formularios", "avisos", "tabela", "thread"]) {
    await expect(light.locator(`[data-section="${s}"]`)).toBeVisible();
    await expect(dark.locator(`[data-section="${s}"]`)).toBeVisible();
  }

  // o painel escuro realmente usa os tokens escuros (L do oklch de --background) e o claro o oposto
  const lightness = (loc: ReturnType<Page["locator"]>) =>
    loc.evaluate((el) => {
      const v = getComputedStyle(el).getPropertyValue("--background").trim();
      const m = v.match(/oklch\(\s*([\d.]+)(%?)/);
      return m ? Number(m[1]) / (m[2] ? 100 : 1) : NaN;
    });
  expect(await lightness(light)).toBeGreaterThan(0.9);
  expect(await lightness(dark)).toBeLessThan(0.3);

  await expect(light).toHaveScreenshot("design-guide-claro.png");
  await expect(dark).toHaveScreenshot("design-guide-escuro.png");
});

test("design-guide: o seletor de tema mostra um tema só e ignora o cookie do usuário", async ({ page }) => {
  await page.goto("/design-guide?tema=escuro");
  await settle(page);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator('[data-pane="light"]')).toHaveCount(0);
  await expect(page.locator('[data-pane="dark"]')).toHaveCount(1);
  await expect(page.locator('[data-section="cores"]')).toHaveScreenshot("design-guide-cores-escuro.png");

  await page.goto("/design-guide?tema=claro");
  await settle(page);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator('[data-section="cores"]')).toHaveScreenshot("design-guide-cores-claro.png");

  await page.getByTestId("dg-mode-ambos").click();
  await expect(page.locator('[data-pane="light"], [data-pane="dark"]')).toHaveCount(2);
});

test("screenshot diff: telas de entrada do DS", async ({ browser }) => {
  const ctx = await browser.newContext({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 }, locale: "pt-BR" });
  const page = await ctx.newPage();
  await page.goto("/entrar");
  await settle(page);
  await expect(page).toHaveScreenshot("entrar.png");
  await page.goto("/claim/token-que-nao-existe");
  await settle(page);
  await expect(page).toHaveScreenshot("claim-invalido.png");
  await ctx.close();
});

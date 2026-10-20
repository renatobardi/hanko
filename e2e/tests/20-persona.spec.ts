import { expect, test } from "./fixtures";
import { B, hirePersona } from "./helpers";

// Jornada 4 — contratar uma persona (harness fake) e vê-la na lista.
test("persona: contratar a Ada com o adapter fake", async ({ page }) => {
  const slug = await hirePersona(page, "Ada");
  await expect(page.getByRole("heading", { name: "Ada" }).first()).toBeVisible();
  await page.goto(`${B}/personas`);
  await expect(page.getByRole("link", { name: "Ada" }).first()).toBeVisible();
  expect(slug).toBe("ada");
});

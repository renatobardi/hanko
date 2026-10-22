#!/usr/bin/env node
// Rasteriza docs/brand/social-card.html nos PNG da prévia social do repositório (1280×640).
//
// Uso: node scripts/gen-social-card.mjs   (requer playwright com Chromium; se estiver
// instalado globalmente: NODE_PATH="$(npm root -g)" node scripts/gen-social-card.mjs)
// Gera em docs/brand/: social-card.png, social-card-dark.png, social-card-pt.png,
// social-card-pt-dark.png e social-card-guides.png (com a margem de segurança visível).
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

// require (e não import) para respeitar NODE_PATH.
const { chromium } = createRequire(import.meta.url)("playwright");

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "docs", "brand");
const src = pathToFileURL(path.join(dir, "social-card.html")).href;

const variants = {
  "social-card": "",
  "social-card-dark": "theme=dark",
  "social-card-pt": "lang=pt",
  "social-card-pt-dark": "lang=pt&theme=dark",
  "social-card-guides": "guides=1",
};

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 640 }, deviceScaleFactor: 1 });
for (const [name, query] of Object.entries(variants)) {
  await page.goto(query ? `${src}?${query}` : src);
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: path.join(dir, `${name}.png`) });
  console.log(`docs/brand/${name}.png`);
}
await browser.close();

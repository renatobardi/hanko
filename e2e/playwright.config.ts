import { defineConfig, devices } from "@playwright/test";
import { OWNER_STATE } from "./harness/state";

// Jornadas críticas do Hanko (ADR-0058 §D). O harness (harness/global-setup.ts) sobe o binário contra um Postgres
// real, com o adapter fake. Duas fases: "setup" (claim do primeiro dono + empresa de teste), que grava a sessão do
// dono, e "jornadas", que a reaproveitam. Um worker só: as jornadas compartilham a mesma instalação.
//
// Chromium: o Playwright procura o navegador em PLAYWRIGHT_BROWSERS_PATH (a versão 1.56 usa o chromium-1194). Para
// apontar um Chromium já instalado, defina PW_CHROMIUM_PATH.

const port = Number(process.env.E2E_PORT ?? 17420);
const executablePath = process.env.PW_CHROMIUM_PATH || undefined;

export default defineConfig({
  testDir: "./tests",
  globalSetup: "./harness/global-setup.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: {
    timeout: 10_000,
    toHaveScreenshot: { maxDiffPixelRatio: 0.01, animations: "disabled", caret: "hide" },
  },
  // as baselines são só de Linux/Chromium (a fonte Inter é servida localmente, então o render é estável)
  snapshotPathTemplate: "{testDir}/__screenshots__/{testFilePath}/{arg}{ext}",
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    locale: "pt-BR",
    timezoneId: "America/Sao_Paulo",
    colorScheme: "light",
    reducedMotion: "reduce",
    serviceWorkers: "block", // o PWA registra um SW; nas jornadas ele só atrapalharia
    viewport: { width: 1280, height: 900 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
    launchOptions: { executablePath },
  },
  projects: [
    { name: "setup", testMatch: /.*\.setup\.ts/, use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, launchOptions: { executablePath } } },
    {
      name: "jornadas",
      testMatch: /.*\.spec\.ts/,
      dependencies: ["setup"],
      use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 }, storageState: OWNER_STATE, launchOptions: { executablePath } },
    },
  ],
});

import { expect, test as base } from "@playwright/test";

// `test` das jornadas: além do padrão, falha qualquer jornada em que a página lance um erro de JavaScript não tratado
// (um erro de sintaxe em hanko.js, por exemplo, desliga todo o JS da tela e só aparece como "ilha não carregou").
export const test = base.extend<{ noPageErrors: void }>({
  noPageErrors: [
    async ({ page }, use) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await use();
      expect(errors, "o navegador não pode ter erros de JavaScript não tratados").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

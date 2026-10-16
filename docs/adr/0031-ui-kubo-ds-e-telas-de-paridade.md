# ADR-0031 — UI alinhada ao Kubo DS e telas de paridade
Data: 2026-10-16 · Status: aceita

## Contexto
A UI do host (ADR-0005, adendo de 2026-10-14) usava tokens próprios "inspirados" no Kubo DS: fonte que não era carregada, nomes de variável diferentes, cards com borda, raios e tamanhos de fonte soltos, estilos inline nos templates. O canvas de design do Hanko (Kubo DS) e a comparação com a categoria apontaram também telas que faltavam: quadro de tasks, visões salvas, abas de projeto e workspace, timeline, painel ao vivo, ambientes/adapters/plugins, export/import pela UI e times prontos.

## Decisão
1. **Tokens e fonte do DS como fonte única.** `hanko.css` usa os nomes e valores OKLCH do Kubo DS (`--background`, `--foreground`, `--card`, `--muted`, `--border`, `--ring`, `--sidebar-*`, `--radius-*`, `--text-*`), Inter Variable servida pelo binário (sem CDN) e cards com anel em vez de borda. Extensões do Hanko ficam explícitas e derivadas do DS quando possível: âmbar de gate, terminal, tint de erro, hover de linha, rampa de gráficos invertida no escuro. Estilos inline viram utilitários na escala de espaço do DS (4/8/12/16/24).
2. **Glifo por persona.** `personas.icon` (migration 00026), catálogo fechado em `domain.PersonaIcons` (18 glifos, padrão `bot`), validado no service, na API e no import. A UI resolve o glifo pelo nome via contexto de render; SVGs (organograma estático, ilha React, timeline) usam um sprite de `<symbol id="pg-…">` e `<use>`, sem duplicar desenhos.
3. **Tema explícito.** `POST /tema` guarda `hanko_theme` (claro/escuro; "sistema" apaga) e o layout põe `data-theme` no `<html>`; sem cookie vale `prefers-color-scheme`.
4. **Quadro e visões de tasks sem nova ilha.** O quadro (`?visao=quadro`) é HTML do servidor; arrastar usa HTML5 drag-and-drop em `hanko.js` e faz o mesmo POST do formulário da task (contrato htmx: erro volta para `#flash` e o card é devolvido). Agrupar/ordenar/filtrar e a aba Minhas são parâmetros da URL; a última visão fica no cookie `hanko_tasks`. O teto de 3 ilhas (ADR-0005) continua.
5. **Uma lista, três páginas.** `buildTasksVM` + `TasksBody` servem `/tasks`, a aba Tasks do projeto e a do workspace (`taskScope` com caminho, campos fixos e filtro).
6. **Timeline e ao vivo renderizados no servidor.** Timeline em SVG (faixas por persona, barras por execução, janelas de 1/7/30 dias); painel ao vivo com polling htmx de 5 s e o fim do log por `ListRunEventsBefore`.
7. **Ambientes, Adapters e Plugins mostram só o real.** `ui.Deps.Platform` (sem segredos) descreve a instalação; Ambientes faz verificações reais (raiz gravável, binários no PATH); Adapters lista os harnesses com estado, personas e preços; Plugins lista os pontos de extensão existentes. Ambientes remotos e plugins de terceiros não existem e a tela diz isso.
8. **Export/Import pela UI via API.** A tela chama os endpoints de ADR-0022 com a sessão do navegador (mesmos limites e vagas da CLI); não há caminho paralelo.
9. **Times prontos sem tabela.** O catálogo é código (`teamCatalog`); instalar contrata as personas pelo service em ordem e resolve slug com sufixo.
10. **Troca da própria senha.** `ChangeOwnPassword` (login local) confere a atual, usa o `PasswordGate`, encerra as sessões e fica na trilha de admin.

## Consequências
- Configurações ganha seções (Geral, Ambientes, Adapters, Plugins, Exportar e importar); Configurações sai do grupo Organização e fica no rodapé da sidebar.
- O cookie `hanko_theme` e o `hanko_tasks` são preferências de navegador, HttpOnly e Lax como o de idioma.
- Ficam para o roadmap, por exigirem modelo de dados novo: cards de status por persona, sala com várias personas, cases/pipelines/fila de revisão com aprendizado, estúdio e fontes de skills, apps/gateways/perfis de ferramentas, ambientes remotos (SSH, sandbox) e plugins de terceiros.

# ADR-0026 — Ilhas React: build, versões e as três primeiras ilhas
Data: 2026-10-15 · Status: aceita

## Contexto
O ADR-0005 reservou até três ilhas React para as peças que o host (templ + htmx) não faz bem: o log de uma run com
10 mil+ eventos, o organograma interativo e comentários ancorados a um trecho de texto. Faltava decidir como se
constrói e versiona o JavaScript sem trazer Node para o runtime, como a ilha recebe dados e textos do host, e qual o
orçamento de tamanho.

## Decisão
1. **Build.** `ui/islands/` tem o fonte em TypeScript/TSX, `package.json` com versões **exatas** e `package-lock.json`
   commitado. O bundle sai de `ui/islands/build.mjs` (API JS do **esbuild**, devDependency) para
   `internal/ui/static/islands/`, é **commitado** e embutido no binário por `go:embed`: quem só compila o Go não precisa
   de Node. Isto ajusta o ADR-0005 ("esbuild via Go"): o pacote Go do esbuild não faz parte das dependências do projeto
   e o runner de testes dos componentes (vitest + jsdom) já exige Node em dev/CI, então um só caminho (npm) é mais simples.
   `make islands` roda `npm ci`, typecheck, testes e build; `make islands-test` só typecheck e testes.
2. **Versões fixas.** react / react-dom 19.3.1, esbuild 0.28.2, typescript 5.9.3, vitest 5.0.4, jsdom 26.1.0,
   @testing-library/react 16.3.3. Atualizar é um commit próprio (lockfile + bundle regerado).
3. **Saída determinística.** ESM, minificada, `target es2020`, **sem sourcemap**, nomes fixos por ilha
   (`run-log.js`, `org-chart.js`, `anchor-editor.js`) e um chunk compartilhado `vendor-<hash>.js` com o React (code
   splitting do esbuild; o hash é do conteúdo). O CI (Node 22) roda `npm ci`, typecheck, testes e build e depois
   `git diff --exit-code -- internal/ui/static/islands` mais a checagem de arquivos novos: bundle desatualizado
   quebra a build. O build apaga a pasta antes (chunk antigo não fica para trás).
4. **Contrato host/ilha.** O host renderiza `<div data-island="nome" data-…>` e o `hanko.js` faz `import()` de
   `/static/islands/<nome>.js?v=<versão>` e chama `mount(el)`. A lista de nomes aceitos é fechada e um teste a compara
   com os arquivos de `static/islands` (teto de 3: a 4ª ilha exige ADR). Props: `data-*`, com JSON onde precisa. **Textos**:
   o host traduz com `i18n.T` e entrega um JSON em `data-msgs` (chave técnica → texto já no idioma); o JS não tem catálogo,
   e as strings novas entram em `internal/i18n/en_ilhas.go` como qualquer outra. **Sem JS**: os filhos do elemento são a
   versão htmx/SVG atual e ficam como estão se o bundle não carregar. A ilha é folha: não roteia, não lista, só chama
   a API REST do host (mesma origem, cookie de sessão; CSRF pelo `Sec-Fetch-Site` que a API já exige).
5. **Segurança.** O host não define CSP hoje. As ilhas já cumprem `script-src 'self'` (módulos same-origin, sem script
   inline, sem `eval`, sem recurso externo) para o dia em que houver. Esconder um controle por papel (`data-can-edit`,
   `data-can-comment`) é só ergonomia: quem decide é o service (ADR-0021).
6. **Log de run virtualizado (`run-log`).** Janela virtualizada de linhas de altura fixa (24 px; linha expandida ganha
   altura extra fixa), 10 mil+ eventos sem travar. Carga inicial pelo **fim** do log e histórico sob demanda, por
   cursor de `seq`: `GET /runs/{id}/events` ganha `tail=true` (últimos N) e `before=<seq>` (N anteriores), sempre em
   ordem crescente; `after` continua sendo a página para frente (`after` não combina com `before`/`tail`: 422).
   O resto vem do SSE `GET /runs/{id}/stream?after=<último seq>` (o servidor repete o que falta do banco). **Sem perda
   nem duplicata**: a chave de dedupe é `seq` (único por run); páginas e SSE se sobrepõem à vontade. Segue o fim e
   **pausa** ao rolar para cima (botão "ir para o fim"); filtro por tipo (chips com contagem), busca no texto com
   destaque, expandir texto completo e payload de tool call. Ao ficar terminal, fecha o SSE.
7. **Organograma interativo (`org-chart`).** Árvore recebida em `data-nodes`; layout calculado no cliente com a
   mesma geometria do host. Arrastar uma persona sobre outra faz `PATCH /personas/{slug}` com `reports_to` (soltar
   na faixa "topo" faz `clear_reports_to`); o cliente recusa laço antes de chamar e o service o recusa de novo (422),
   caso em que a ilha desfaz o movimento e mostra a mensagem. Zoom (botões e Ctrl + roda), pan arrastando o fundo,
   colapsar ramos, persona com run ativa pulsa. `data-can-edit` só vale para admin/owner; viewer/member veem, não arrastam.
8. **Comentários ancorados (`anchor-editor`).** Migration 00017: `task_comments.anchor jsonb` (nulo = comentário
   comum). A âncora guarda `target` (`task_description` ou `run_summary` + `run_id`), a **citação**, offsets
   `[start,end)` em **code points** e o hash do documento (SHA-256 truncado) — o documento é normalizado para `\n`,
   como o navegador o mostra. O service valida ao comentar (a citação tem que existir no documento-alvo, que
   pertence à task) e grava offsets/hash canônicos. **Re-localização pelo texto**: se o hash bate, vale o offset
   (`exact`); se o documento mudou, procura-se a citação e vale a ocorrência mais próxima do offset antigo (`moved`);
   se não achar, `lost` e a thread mostra "âncora perdida" (o comentário não some). O estado vem calculado pelo
   service em `ListComments` (`anchor_state` na API). API (`anchor` opcional em `POST …/comments`), CLI
   (`hanko task comment --quote [--quote-run]`; `task show` exibe a citação) e MCP (`quote`, `quote_run`) aceitam a
   âncora. A ilha mostra o texto-fonte do documento com os trechos destacados; selecionar um trecho abre o comentário;
   a thread do host mostra a citação em cada comentário ancorado.
9. **Orçamento de tamanho (gzip, nível 9)** — verificado por `build.mjs`, que falha acima do teto:

   | Arquivo | gzip | teto |
   |---|---|---|
   | `vendor-<hash>.js` (React + react-dom, compartilhado) | ~67,6 KB | 75 KB |
   | `run-log.js` | ~3,8 KB | 12 KB |
   | `org-chart.js` | ~3,4 KB | 12 KB |
   | `anchor-editor.js` | ~2,5 KB | 12 KB |

   Uma página paga o `vendor` uma vez (cache do navegador) mais a sua ilha. Dependência nova numa ilha precisa caber no
   teto da ilha; se não couber, justifique no PR e suba o teto no `build.mjs`.
10. **Como adicionar uma ilha** (só depois de um ADR que suba o teto de 3): criar `ui/islands/src/<nome>/index.tsx`
    exportando `mount(el)`, registrar a entrada em `build.mjs` e o nome em `ISLANDS` no `hanko.js`, montar
    `<div data-island="<nome>" …>` no `.templ` com a versão sem JS dentro, traduzir os textos no host (`data-msgs`),
    escrever testes (vitest) e rodar `make islands`.

## Consequências
- Node volta só como ferramenta de dev/CI; o binário e o `go build` seguem sem Node. Mudar uma ilha exige rodar
  `make islands` e commitar o bundle (o CI confere).
- A tela da run mantém a conexão SSE do host (cabeçalho em htmx) além da da ilha: duas conexões por run aberta, e o
  servidor renderiza as linhas HTML mesmo quando a ilha as ignora. Aceito por ora; sai quando o cabeçalho também
  virar dado.
- O editor ancorado mostra o texto-fonte (Markdown cru), não o renderizado: âncoras são por texto, e o renderizado não
  tem offsets estáveis. Sem JS, o host segue mostrando o Markdown renderizado e a thread com as citações.
- Âncoras não entram no export/import de empresa (referenciam runs e offsets locais); o comentário é exportado sem elas.
- Mover persona no organograma é só por arrasto (ponteiro); o teclado continua editando `reports_to` na página da persona.
- A busca do log filtra por texto e tipo; o conteúdo de `payload` não é pesquisado (só exibido ao expandir).

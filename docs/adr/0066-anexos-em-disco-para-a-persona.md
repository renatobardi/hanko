# ADR-0066 — Anexos em disco para a persona
Data: 2026-10-23 · Status: aceita · Altera: ADR-0052 (como a persona lê anexos)

## Contexto
O dono anexou uma imagem JPEG de 3,3 MiB num chat e pediu para a persona confirmar que conseguia lê-la. Ela respondeu
que só recebeu os metadados. A causa está na tool `hanko_attachment_get` (ADR-0052):

- binário volta só até 1 MiB, e em base64 dentro de um campo JSON (`content_base64`);
- o modelo não enxerga base64 como imagem nem como PDF: para ele é uma sequência de caracteres;
- 1 MiB em base64 dá ~1,4 milhão de caracteres, bem acima do que o harness repassa de uma resposta de tool.

Ou seja, a persona não enxergava nenhuma imagem ou PDF anexado, de qualquer tamanho. Com anexos de até 64 MiB
(ADR-0065), o caminho pelo MCP piora. Os harnesses, por outro lado, já leem arquivos do diretório de trabalho com as
próprias ferramentas: o Read do claude lê texto, imagem e PDF.

## Decisão
1. **Antes da run, o heartbeat grava os anexos no diretório de trabalho**, em `.hanko-anexos/<run>/<nome>`. São os
   mesmos anexos que o briefing já lista (task ou conversa, até 30), lidos do BlobStore pelo service. A pasta da run é
   própria (duas runs no mesmo workspace não se pisam) e é apagada no fim da run; a pasta `.hanko-anexos` sai junto
   quando fica vazia.
2. **Por que dentro do diretório de trabalho:** é o único caminho que a persona alcança em qualquer harness sem
   permissão extra, e o diretório é sincronizado para os ambientes remotos (ADR-0037), então o caminho relativo vale
   também lá.
3. **Nunca no Git:** a pasta recebe um `.gitignore` com `*`, então os arquivos não entram num commit do workspace nem
   aparecem no `git status`.
4. **Nome em disco:** só o nome base do anexo (sem diretórios nem ponto inicial). Dois anexos com o mesmo nome recebem
   o começo do id na frente.
5. **Teto por run: 512 MiB em disco.** Os anexos que passarem disso, ou que não puderem ser lidos ou gravados, viram
   um aviso no log da run e seguem só pelo MCP.
6. **Briefing:** cada anexo gravado traz `· arquivo \`.hanko-anexos/<run>/<nome>\``, e a seção diz para abri-lo com a
   ferramenta de leitura de arquivos, sem movê-lo nem incluí-lo em commit. Sem caminho, vale o MCP como antes.
7. **A tool MCP continua** para texto e binário pequeno. A descrição e a resposta de "grande demais" apontam para o
   arquivo em disco.
8. Runs de revisão e testes do estúdio de skills não recebem os arquivos (não listam anexos).

## Consequências
- A persona passa a ver imagens e PDFs anexados, no tamanho que o upload aceita.
- Cada run com anexos escreve no disco até 512 MiB temporários; com o volume de workspaces no mesmo disco do LXC, o
  `hanko doctor` já avisa com pouco espaço.
- Uma queda do processo no meio da run pode deixar `.hanko-anexos/<run>/` para trás; o `.gitignore` impede que isso
  vá para um commit, e a próxima run usa outra pasta.

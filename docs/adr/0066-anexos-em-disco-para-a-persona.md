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

## Adendo (2026-10-23) — PDF grande e compositor
O primeiro uso real (um PDF de 23 MiB) mostrou o limite do harness: a ferramenta de leitura de arquivos recusa PDF acima de
~20 MB, e a imagem do container não tinha nenhum utilitário de PDF, então a persona não leu nada.
1. **Utilitários na imagem:** `poppler-utils` (`pdfinfo`, `pdftotext`) e `qpdf` entram no `Dockerfile.agents`.
2. **PDF grande é extraído e dividido antes da run:** acima de 15 MiB ou de 100 páginas, o heartbeat grava ao lado do
   original o texto inteiro (`<nome>.txt`, `pdftotext -layout`) e partes (`<nome>.parte-NN.pdf`) feitas por um único
   `qpdf --split-pages` (um só parse do arquivo). O número de partes é o maior entre 1 por 20 páginas e 1 por 10 MiB,
   até 50; assim um escaneado pesado de 15 páginas também é dividido (em partes de 5). A divisão é tudo ou nada: se o qpdf
   falha ou as partes passam do que ainda cabe no teto de disco, nenhuma parte fica e o briefing não as promete. Aviso do
   qpdf (saída 3) não é falha (`--warning-exit-0`). Cada utilitário tem timeout de 2 min e o PDF, 5 min no total. O que
   as partes e o texto ocupam entra no teto de 512 MiB por run. Os nomes gerados (`.txt` e `.parte-NN.pdf`) são reservados
   no mapa de nomes da run, então nunca colidem com outro anexo (um `Plano.txt` ao lado de `Plano.pdf`).
3. **Briefing:** o anexo traz o caminho do texto ("leia primeiro") e o padrão das partes, e diz para abrir só as partes com
   figura ou tabela que importam. PDF escaneado (sem texto extraível) recebe só as partes, com aviso no log da run.
4. **Sem os utilitários** (instalação fora do container) nada quebra: o PDF segue inteiro e o briefing não promete partes.
5. **Compositor (chat, sala, comentário de task):** colar imagem ou arquivo (Ctrl/Cmd+V, quando não há texto na área de transferência) anexa e mostra a miniatura, com nome
   e tamanho ao passar o mouse e um X para remover; anexos enviados aparecem como miniaturas (imagem até 5 MiB; o resto
   vira cartão com ícone e extensão). Enter envia e Shift+Enter quebra a linha (no celular o Enter quebra a linha). A caixa
   fica fixa embaixo da tela por padrão, com um alfinete para soltá-la (a escolha fica no navegador), e setas flutuantes
   levam ao início e ao fim da conversa, que abre no fim e acompanha mensagem nova só se o leitor estiver perto do fim.

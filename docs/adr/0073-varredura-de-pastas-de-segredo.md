# ADR-0073 — Varredura das pastas `hanko-secret-*` no boot
Data: 2026-10-24 · Status: proposta (aguarda aceite do Bardi) · Issue: #58 (parte de #54) · Completa: ADR-0072 (pendência 1)

## Contexto
O `${secretfile:}` cria um diretório `hanko-secret-*` em `os.TempDir()` para cada run (ADR-0072, desvio 1), e a limpeza
da run o apaga. Se o processo morrer no meio (kill -9, falta de memória), a pasta fica com o arquivo `0600` do segredo
em claro. O ADR-0072 deixou a varredura como pendência: apagar a pasta de um processo em uso derrubaria a run dele, e o
limite de idade não estava definido.

## Decisão
1. **Quando.** No boot do servidor, uma vez, logo depois de `migratePersonaEnvAtBoot` e antes de o heartbeat subir
   (`internal/app/app.go:189`; o heartbeat sobe em `internal/app/app.go:241`).
2. **O que entra.** Só diretórios de `os.TempDir()` cujo nome começa com `hanko-secret-`. Arquivo solto com o mesmo prefixo
   fica. Um diretório que não é `hanko-secret-*` nunca é tocado.
3. **Limite de idade.** A idade do diretório (mtime) tem de passar de `run_timeout + 1 h`. É o mesmo limite com que o
   heartbeat resgata uma run presa (`internal/heartbeat/heartbeat.go:184`, `rescue := opts.RunTimeout + time.Hour`). Uma
   run que ainda vive além desse teto já foi dada como presa e resgatada, então a sua pasta já não tem dono.
4. **Falha não derruba o boot.** Erro ao ler o diretório, ou ao apagar uma pasta, vira log `WARN` e a varredura segue; o
   próximo boot tenta de novo.

## Consequências
- Pastas de runs mortas saem no próximo boot, não na hora. O prazo máximo é o próprio boot seguinte.
- Com mais de uma instância do Hanko no mesmo host e `TMPDIR` compartilhado, o boot de uma pode apagar a pasta de uma
  run da outra só se essa run já passou do próprio teto de resgate. Não há como saber qual instância é dona de cada pasta
  sem gravar o dono nela; isso fica fora desta decisão.
- O limite usa a idade da pasta, não a do arquivo dentro dela. Uma run que grava dentro da pasta a qualquer momento não
  muda o mtime do diretório, só criar ou apagar entradas muda. A pasta nasce com a run e o arquivo é criado logo em
  seguida, então a idade do diretório é a da run.

## Alternativas descartadas
- **Varredura periódica, durante a execução.** Descartada: aumenta a chance de tocar em pasta de run viva, e o boot já
  cobre o caso.
- **Gravar um arquivo de dono (PID) na pasta.** Descartada nesta entrega: exige checar se o PID ainda vive e não resolve o
  caso de PID reaproveitado. Fica como evolução se o limite de idade se mostrar insuficiente.

# ADR-0061 — Postgres embutido só para desenvolvimento e trial
Data: 2026-10-22 · Status: aceita · Complementa: ADR-0011 · Item do plano: #47

## Contexto
Experimentar o Hanko exige um Postgres. O plano pede `hanko serve --embedded-pg` para reduzir isso a um comando, sem
mudar que **produção usa Postgres externo, única dependência de runtime** (ADR-0011).

## Decisão
1. `hanko serve --embedded-pg [--pg-dir D] [--pg-bin-dir B] [--pg-port P]` sobe um cluster local descartável e roda o
   servidor em cima dele. Recusa se `HANKO_DATABASE_URL` está definida (ou é banco externo, ou é embutido) e imprime aviso de
   que não serve para produção.
2. **Abordagem: usar os binários do PostgreSQL já instalados na máquina** (`initdb`, `pg_ctl`; procurados em `--pg-bin-dir`,
   PATH e lugares comuns de Debian, Homebrew, RPM, Postgres.app; versão mínima 14, a recomendada é a 16/17). O Hanko não
   baixa nem empacota o Postgres. O cluster mora em `<HANKO_HOME>/pg`: senha aleatória (scram-sha-256, só em arquivo 0600 e
   `--pwfile`, nunca em argv), escuta só em 127.0.0.1, sem socket unix, porta estável entre execuções. `fsync=off`
   (descartável por definição). Um cluster já no ar (outro `hanko serve`) é reaproveitado e não é derrubado ao sair; o que o
   processo subiu é parado com `pg_ctl -m fast` no encerramento. Recusa rodar como root (o Postgres também recusa).
3. Banco `hanko` criado no primeiro uso; as migrations rodam no boot como sempre. O modo de autenticação é o configurado
   (padrão authenticated, com o link de primeiro dono impresso no boot).

## Alternativas descartadas
- Biblioteca que baixa os binários do Postgres em tempo de execução (por exemplo a que busca no Maven): dependência Go nova,
  rede obrigatória no primeiro uso, binário não auditado e testes dependentes da rede. Fica como evolução possível atrás da
  mesma interface (`syscmd.Runner` + `FindBinaries`) se o `install.sh` (#54) precisar de zero-instalação; exigiria ADR próprio.
- SQLite ou outro motor para trial: o produto usa recursos do Postgres (jsonb, advisory locks, River, full-text); um segundo
  motor dobraria a matriz de testes e esconderia bugs.
- Empacotar o Postgres no binário: tamanho, licença e superfície de segurança desproporcionais para dev/trial.

## Consequências
Sem dependência Go nova. Testes usam `syscmd.Fake`: nenhum baixa ou executa Postgres. Quem não tem Postgres instalado recebe
a mensagem com os comandos de instalação por sistema. Em produção a flag simplesmente não é usada.

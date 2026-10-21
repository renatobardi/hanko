# ADR-0057 — Acesso e ciclo de vida da empresa: arquivar/excluir, import com prévia e merge, login pelo navegador, pedidos de entrada
Data: 2026-10-17 · Status: aceita

## Contexto
Uma empresa só nasce (ADR-0010) e o arquivo portátil (ADR-0022) só cria empresa nova, sem prévia. O acesso de um segundo humano
depende de convite por e-mail (ADR-0021), a CLI exige copiar uma API key à mão e o dono da instalação não tem uma tela com as contas.
Este ADR cobre os itens #25, #26 e #28 do marco M16b (`docs/PLANO-DECISOES.md`); revisa o ADR-0021 (acrescenta pedidos de entrada) e
estende o ADR-0022 (prévia e merge). Em conflito, vale este.

## Decisão

### A. Ciclo de vida da empresa (#25)
1. **Editar.** `PATCH /companies/{c}` muda nome, fuso e descrição (`companies.description`, ≤ 2000) — admin. O **prefixo das tasks**
   só muda enquanto a empresa tem **zero tasks** (a chave `HNK-12` aparece em comentários, commits e links; mudar depois quebraria
   referências); com tasks, `ErrConflict`. O slug não muda (é endereço). Atividade `company.updated` com os campos alterados.
2. **Arquivar** (`POST /companies/{c}/archive`, admin). Na mesma transação: personas ativas/ociosas/rodando/aguardando/em erro
   passam a `paused` **sem `pause_reason`** (o retomar-por-orçamento do ADR-0019 não as reativa), runs `queued`/`running` são
   canceladas (mesmo caminho de `CancelRun`), fluxos ligados são desligados. Um **snapshot** (`archive_snapshot`: ids das personas e
   fluxos que o arquivamento mexeu) e `archived_at`/`archived_by` ficam na empresa. Atividade `company.archived`.
3. **Read-only por um ponto só.** Toda escrita de domínio grava `activity_event` na mesma transação (AGENTS.md); `txc.activity`
   passa a recusar com `ErrConflict` ("empresa arquivada") qualquer evento de empresa arquivada, exceto os `company.*` e o
   encerramento de runs (`run.*`, para uma run em voo poder fechar). A transação inteira desfaz, então nenhum caminho de escrita
   (API, UI, CLI, MCP, heartbeat) precisa lembrar da regra. Leituras seguem normais; o despacho já não acorda persona pausada, e
   os agendadores (fluxos, cards de status, memória) ignoram empresa arquivada nas consultas "devidas". A UI mostra a faixa
   "Empresa arquivada — somente leitura" e esconde os controles de escrita.
4. **Restaurar** (`POST /companies/{c}/restore`, admin). Limpa `archived_at`. **Nada volta a trabalhar sozinho**: personas e fluxos
   continuam pausados/desligados. `resume=true` reativa exatamente os que o snapshot registra (e só os que ainda estão como o
   arquivamento os deixou). Atividade `company.restored`.
5. **Excluir** (`DELETE /companies/{c}`, **só owner** da empresa, **só arquivada**, corpo `{"confirm":"<nome exato>"}`). Antes de
   apagar, o service monta o **export automático** (`WithRuns`, e artefatos quando cabem no teto de 20 MiB), comprime e grava em
   `company_deletions` (sem FK para a empresa; guarda slug, nome, quem apagou, sha256). Como a empresa está arquivada, o export é
   consistente. Só depois a empresa é apagada (`ON DELETE CASCADE` de todo o domínio) e o conteúdo de blobs externos é removido.
   `GET /company-deletions` e `GET /company-deletions/{id}/export` devolvem o arquivo (owner da instalação ou quem apagou); o
   arquivo é um `hanko.company.v1` comum e pode ser reimportado. A atividade `company.deleted` fica em `admin_audit` (a empresa
   some, e com ela seu `activity_event`). Não há exclusão de empresa ativa: arquivar é o passo deliberado.
6. **Listagens.** `GET /companies` devolve arquivadas com `archived_at`; `?archived=false` as oculta. A UI as separa em "Arquivadas".

### B. Import com prévia e merge (#26)
7. **Dry-run.** `POST /companies/import?dry_run=true` roda a **mesma validação** de ponta a ponta (`planImport`) e devolve o que
   aconteceria **sem gravar nada**: contagens, lista de itens com a ação prevista e avisos (caminhos de workspace limpos, segredos
   `${secret:…}` a recriar, integrações que entram desligadas, personas pausadas, histórico que não vai para empresa existente).
   Erro de validação é o mesmo 400/422 do import real. A UI tem a tela de prévia (`/importar`): envia o arquivo, mostra a prévia
   e só então confirma.
8. **Destino.** Empresa **nova** (padrão, ADR-0022 inalterado) ou **existente** (`into=<empresa>`, admin dela, não arquivada).
9. **Merge em empresa existente** importa só a **configuração**: workspaces, projetos, assinaturas, personas (com organograma,
   revisor e skills), fluxos, skills e integrações. Tasks, comentários, execuções e artefatos **não** entram numa empresa que já
   tem sua própria numeração e histórico (a prévia avisa quantos ficaram de fora); eles seguem exclusivos de empresa nova. O
   nome, orçamento e fuso da empresa de destino não mudam.
10. **Conflito por item.** A chave é slug (nome, para assinaturas). Para cada item que já existe no destino a ação é **pular**
    (`skip`: o item do destino fica e as referências do arquivo apontam para ele), **renomear** (`rename`: entra como `slug-2`,
    `slug-3`… e as referências apontam para o novo) ou **substituir** (`replace`: o item do destino é atualizado no lugar, mesmo id,
    com o conteúdo do arquivo; em assinaturas os períodos não mudam; em persona o status de execução não muda). Padrão por tipo
    de conflito `on_conflict` (padrão `skip`) e exceção por item em `resolve=<kind>/<chave>=<ação>` (repetível). Personas e
    integrações importadas entram pausadas/desligadas como no ADR-0022, mesmo quando `replace` (nunca liga nada sozinho). O
    organograma resultante é revalidado (ciclo → `ErrConflict`, nada gravado).
11. **Reuso.** O motor (`planMerge` → itens com ação → `applyMerge`) é independente de onde veio o documento: o instalador de
    pacotes de time do M17 (#24) chama o mesmo caminho com um subset do company.v1.
12. **Atividade.** Merge grava `company.import_merged` (contagens e itens por ação) na empresa de destino, na mesma transação.

### C. Acesso (#28)
13. **`hanko login` pelo navegador.** Fluxo de dispositivo, sem segredo na URL pública do servidor:
    1. `POST /api/v1/cli-auth` (público, limitado por IP, no máximo 200 pedidos abertos) com `device` (nome do dispositivo) e `scope`
       (`full` | `read`) cria um pedido (`cli_auth_requests`, validade de 10 min) e devolve `code` (6 bytes aleatórios,
       exibível), `poll_token` (32 bytes), `verify_path` (`/cli-auth/<code>`) e o intervalo de consulta. Só os hashes vão ao banco.
    2. A CLI abre `<url>/cli-auth/<code>` no navegador (ou imprime o link) e consulta `POST /api/v1/cli-auth/poll` com o `poll_token`.
    3. A página `/cli-auth/<code>` exige **sessão** (login normal; `API key` não serve), mostra dispositivo, escopo e conta, e
       tem Autorizar/Negar (POST same-origin). Código desconhecido, expirado ou já decidido: mesma resposta 404.
    4. Autorizado, o próximo `poll` cria a API key (nome = dispositivo, `scope`, `device`) **uma única vez** (consumo atômico) e a
       entrega; depois disso o pedido é `consumed`. Negado/expirado devolve o estado. A API key não fica em claro em lugar algum.
    5. A CLI grava `{url, token, device}` em `$XDG_CONFIG_HOME/hanko/contexts.json` (modo 0600; contexto `default` ou `--name`) e
       passa a usá-lo quando `--token`/`HANKO_TOKEN` não vêm. `hanko context add/use/list` (#68) lê o mesmo arquivo.
14. **Escopo da API key.** `api_keys.scope`: `full` (padrão; o que já existia) ou `read`. Com `read` o Guard só aceita métodos
    seguros em `/api/…` e recusa `/mcp`; uma chave de leitura não cria nada nem gera outras chaves. O papel do usuário nas
    empresas continua valendo por cima. `api_keys.device` guarda o nome do dispositivo.
15. **`hanko whoami`** (`GET /api/v1/whoami`): usuário, papel na instalação, como se autenticou (sessão ou API key, com nome,
    dispositivo e escopo da chave) e, em modo trusted, `trusted`. `hanko logout` apaga o contexto local e revoga a chave.
16. **Revogar na UI.** A tela de API keys existente (ADR-0007/0018) passa a mostrar dispositivo, escopo e último uso; revogar já
    existia e vale para as chaves de `login`.
17. **Pedidos de entrada (join requests).** Alternativa ao convite por e-mail: o admin gera um **link público** da empresa
    (`/pedir-acesso/<token>`, 32 bytes, só o hash no banco, um link por empresa; gerar outro invalida o anterior; pode ser
    desligado). Quem abre o link precisa estar logado; o pedido (`company_join_requests`, com mensagem opcional) fica
    `pending`. O **admin aprova escolhendo o papel** (admin só concede `member`/`viewer`; `admin`/`owner` exigem owner,
    como nos convites) ou nega. Um pedido aberto por empresa e usuário; quem já é membro recebe `ErrConflict`. Token
    desconhecido/desligado é 404 sem distinguir. A página responde `Referrer-Policy: no-referrer` e o caminho é mascarado nos logs
    (`auth.RedactPath`). Atividade `join.requested`, `join.approved`, `join.denied`, `join_link.created|revoked` (e `member.joined`
    na aprovação). Em modo trusted não há pedidos (sem identidade).
18. **Usuários da instância.** `GET /api/v1/users` e a tela `/admin/usuarios` (dono ou admin da instalação) listam contas: nome,
    e-mail, provider, papel na instalação, empresas das quais participa, chaves ativas e último login. Somente leitura; as
    operações destrutivas continuam em `hanko auth user …` (ADR-0021, dono da instalação, com `admin_audit`).

## Consequências
- Arquivar é barato e reversível; excluir é deliberado (arquivada, owner, nome digitado, export guardado). O export guardado
  ocupa linha em `company_deletions` (bytea comprimido); a limpeza é manual (`hanko company deletions prune` fica fora deste marco).
- Qualquer evento de atividade novo em empresa arquivada é recusado: quem adicionar escrita de domínio sem `activity_event`
  escapa da regra de read-only; o teste de arquivamento percorre as escritas principais para pegar regressão.
- O merge **não** é um sincronizador: não apaga o que não está no arquivo e não importa histórico.
- O `poll` do login é público; o abuso fica limitado pelo teto de pedidos abertos, pela validade curta e pelo limitador por IP
  (estado por processo, como o do ADR-0018).
- Sem dependência Go nova.

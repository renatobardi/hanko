# ADR-0032 — Segredos da empresa, cofre externo e settings da instância
Data: 2026-10-17 · Status: aceita

## Contexto
O ADR-0029 cifrou os segredos das integrações (headers de MCP, variáveis do stdio, segredo HMAC de webhook) com
`internal/secretbox`, mas deixou duas lacunas:

1. O `env` da persona (`adapter_config.env`: `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, tokens de MCP do agente...) é
   guardado em **claro** na coluna `personas.adapter_config`. A API e o export já o mascaram, mas o banco, um backup e
   um `pg_dump` o carregam como texto. É o ponto mais fraco do cofre.
2. Segredos não são um recurso: cada integração cifra o que é seu e nada é reaproveitado. Credencial de repositório,
   chave do provedor de modelo e token de MCP viram o mesmo valor copiado em vários lugares, sem rotação central.

Do outro lado, toggles que o operador mudava só por variável de ambiente + restart (ACP, stdio, cotas) pedem uma tela,
com trilha de auditoria, sem tocar em nada sensível.

## Decisão

### A. Segredos como recurso da empresa
1. **Modelo** (migration 00030). Tabela `secrets` (`company_id` em toda linha): `scope` (`company` | `user`), dono
   (`owner_user_id`, só no escopo `user`), `name`, `description`, `provider` (`local` | `vault`), `value_enc`
   (cifrado) ou `external_ref` (caminho no cofre externo), `version` (sobe a cada rotação), `revealed_at`,
   `last_used_at`. Nome: `^[A-Za-z][A-Za-z0-9_.-]{0,63}$`, único por empresa (escopo company) e por empresa + usuário
   (escopo user).
2. **Cifra.** O mesmo `secretbox` do ADR-0029 (AES-256-GCM, `HANKO_SECRET_KEY`, rotação com
   `HANKO_SECRET_KEY_PREVIOUS`). O AAD é `secret/<id>`: copiar o texto cifrado para outra linha não decifra. Sem chave
   no servidor não se cria segredo local. `hanko secret rekey` (e o `rekey` das integrações, que passa a cobrir as duas
   tabelas) reescreve com a chave atual.
3. **Referência.** `${secret:NOME}` (empresa) e `${secret:user/NOME}` (do usuário). Pacote `internal/secretref`
   (análise e expansão, sem I/O). A referência pode estar em: valores de header e de env de **integrações**, valores de
   `adapter_config` da **persona** (`env` e também argumentos), e é a forma reservada para a credencial de repositório
   e para o provider de modelo (#33 e #27 consomem `Service.ResolveSecretText`; hoje o provider de modelo é o `env` da
   persona, que já usa referência). Na gravação a referência é validada (o segredo precisa existir) e referência `user/`
   é recusada em configuração compartilhada (persona, integração): um agente não age "como" um humano.
4. **Resolve no despacho, não antes.** O banco guarda a referência; o valor só existe em memória, durante a run:
   `RunExtras` expande headers/env dos MCPs da empresa; `ResolveAdapterConfig` expande o `adapter_config` da persona;
   a lista de valores resolvidos entra em `adapter.Spec.Secrets` e na redação (o executor embrulha o sink e o resultado
   em `RedactingSink`/`RedactResult`, além do que cada adapter já faz). Segredo ausente em uma integração a pula com
   aviso (padrão do ADR-0029); em `adapter_config` da persona falha a run cedo, com o nome do segredo e nunca o valor.
5. **Corrigir o env em claro.** `HirePersona` (e a importação) troca todo valor de `env` por um segredo
   `persona.<slug>.<VARIAVEL>` e grava só a referência. Para o que já existe: `MigratePersonaEnv` roda no boot quando
   há chave (idempotente, evento `persona.env_migrated` por persona) e por `hanko secret migrate-env`. Sem chave, o
   valor em claro continua funcionando (retrocompatível) e o boot avisa quantas personas ficaram assim.
6. **Revelar uma vez.** Não existe leitura de valor. Duas saídas, ambas auditadas: (a) segredo **gerado pelo servidor**
   (`generate: true`, 32 bytes aleatórios) devolve o valor na resposta da criação/rotação e marca `revealed_at`;
   (b) `reveal` (admin da empresa; o dono, no escopo user) devolve o valor **uma única vez por versão** se `revealed_at`
   é nulo e o marca; a segunda chamada é 409. Rotacionar zera a marca. Tokens de run (personas) nunca revelam.
7. **Segredos de usuário.** Escopo `user`: só o dono lista, revela, rotaciona e apaga; nenhum admin, nem o dono da
   instalação, vê nome ou valor (a consulta filtra por `owner_user_id`). O fato de atividade não leva o nome. O uso em
   run de persona **não** existe nesta entrega (a run não registra o humano que a pediu); o service oferece
   `ResolveUserSecretText` para ações feitas pelo próprio dono. Exige identidade de usuário: no modo trusted o escopo
   user é recusado.
8. **Papéis (ADR-0021).** Listar nomes: member. Criar, rotacionar, apagar, revelar: admin (escopo company). Apagar um
   segredo em uso por persona ou integração é recusado, listando quem usa (`force` apaga mesmo assim).
9. **Fatos e telemetria.** `secret.created|rotated|revealed|deleted` e `persona.env_migrated`, na mesma transação, com
   nome, escopo, provider e versão, nunca valor. Métrica `hanko.secrets.resolved` (por provider e resultado) e span
   `hanko.secrets.resolve`, sem atributo de valor.
10. **Não vaza.** O valor nunca entra em: atividade e webhooks de saída, export (o export leva só a referência, e a
    máscara de `MaskConfig`/`ScrubConfig` preserva `${secret:...}` por ser nome e não valor), briefing, log do servidor,
    evento de run, span OTel, mensagem de erro, resposta de listagem. Testes de não-vazamento com valor sentinela
    cobrem cada um desses pontos.
11. **Modelo de ameaça (honesto).** Isto protege contra vazamento de banco/backup/export/log e contra papéis abaixo de
    admin. Um admin da empresa que define a configuração de uma persona consegue, por definição, fazer o agente usar o
    segredo; a revelação-uma-vez é trilha de auditoria, não barreira contra quem controla o que roda.

### B. Cofre externo (provider)
12. **Interface.** `internal/vault.Provider` (`Name()`, `Get(ctx, ref) (string, error)`). Um segredo `provider=vault`
    guarda só `external_ref`; o valor é buscado **a cada despacho** (sem cache: rotação no cofre vale na próxima run).
13. **1º provider: API KV v2 do Vault (também servida pelo OpenBao).** Critérios, nesta ordem: (a) rodar auto-hospedado,
    sem conta de nuvem (o Hanko é self-host, ADR-0008); (b) API HTTP simples e estável, testável com `httptest` sem
    SDK (a regra de dependências do ADR-0011); (c) autenticação por token estática basta para a v1 (o token vem de env,
    nunca do banco); (d) presença no parque de quem já tem cofre. Descartados por ora: gerenciadores de segredo de
    nuvem (exigem credencial de nuvem e assinatura própria por serviço), arquivos em disco montados (já cobertos por
    `HANKO_AGENT_ENV_ALLOW`). `external_ref` = `caminho#campo` (campo opcional se o segredo tem uma chave só: `value`).
    Env: `HANKO_VAULT_ADDR`, `HANKO_VAULT_TOKEN`, `HANKO_VAULT_MOUNT` (padrão `secret`), `HANKO_VAULT_NAMESPACE`
    (opcional). Cliente HTTP com timeout de 10 s e limite de 1 MiB na resposta. Sem `HANKO_VAULT_ADDR`, `provider=vault`
    é recusado.
14. **Testes** contra um servidor `httptest` que imita o KV v2; nenhuma chamada real.

### C. `instance_settings` (#66)
15. **Escopo.** Settings são da **instalação**, não de uma empresa (como `users`): a tabela `instance_settings`
    (migration 00031) não tem `company_id`, e a trilha é a tabela própria `instance_setting_audit` (chave, valor
    anterior, novo, autor, instante), na mesma transação, mais um log. É a exceção ao `activity_event` por empresa.
16. **Só o não sensível.** Um registro em código (`internal/settings`) declara cada chave com tipo, padrão, variável de
    ambiente equivalente e grupo. Hoje: `acp.allow`, `acp.commands`, `integrations.allow_stdio`,
    `integrations.allow_private`, `artifacts.max_bytes`, `artifacts.quota_bytes`, `run.reserve_usd`,
    `alerts.silent_factor`, `alerts.run_stuck_after`. O grupo `experimental.*` existe no registro para flags novos.
    Segredo, chave, URL de banco e credencial **não** são chave registrada: a API recusa o que não está no registro, e
    um teste confere que nenhuma chave registrada tem nome de credencial.
17. **Precedência: env > banco > yaml/padrão.** Se a variável de ambiente da chave está definida, ela vence, a tela
    mostra o campo travado ("definido por HANKO_X") e a API recusa a mudança (409). O valor vem em tempo de leitura
    (`Manager.Get*`), então mudar vale sem restart; os pontos de uso (`allowStdio`, política de ACP, limites de
    artefato, reserva de run, limiares de alerta) leem do `Manager`.
18. **Quem.** Dono da instalação (`users.role = owner`); no modo trusted, sem restrição. UI em Configurações
    › Instância, API `/instance/settings`, CLI `hanko settings list|get|set|unset`.

## Consequências
- O `env` da persona deixa de existir em claro no banco assim que o servidor tem chave; a ausência de
  `HANKO_SECRET_KEY` continua sendo o único jeito de operar sem cofre, e o painel do ambiente avisa.
- Um segredo apagado quebra quem o referencia no próximo despacho (a run da persona falha cedo com a causa); por isso o
  apagar lista os usos antes.
- Export/import não carregam valor: quem importa precisa recriar os segredos que as referências citam (o import avisa
  quais nomes não existem).
- Rotação no cofre externo vale sem ação do Hanko; indisponibilidade do cofre falha a run (persona) ou pula a
  integração com aviso, nunca cai para um valor antigo.
- Fica para depois: uso de segredo de usuário por runs (exige registrar o humano solicitante), outros providers de
  cofre, cache com TTL, referência em credencial de repositório e provider de modelo próprio (consumidores do
  `ResolveSecretText`).

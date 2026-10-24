# ADR-0068 — Cofre para agentes: campos, coleções, uso na run, histórico, saúde e portabilidade
Data: 2026-10-24 · Status: proposta · Complementa: ADR-0050 · Issue: #54 (decisões 1–5, 8, 9, 11–14), contrato em #55

## Contexto
O ADR-0050 fez do segredo um recurso da empresa: **um valor** cifrado no servidor (`secretbox`, AES-256-GCM),
citado por `${secret:NOME}` e resolvido **só no despacho** da run. Esse desenho tem cinco limites para quem opera
personas:

1. **Um valor por segredo.** Uma credencial de login (usuário, senha, URL, semente TOTP) vira quatro segredos soltos,
   ligados só pelo nome.
2. **Toda persona alcança todo segredo da empresa.** Quem é admin cita qualquer segredo em qualquer persona. O §11 do
   ADR-0050 admite que nada limita o que uma persona pode usar.
3. **O valor só entra no início da run.** Um código TOTP vence em 30 s. Um segredo descoberto no meio do trabalho não
   tem como chegar à persona.
4. **Rotação sobrescreve** (`db/migrations/00035_segredos.sql`). Uma rotação que quebra a integração não tem volta.
5. **Arquivo não cabe**: chave SSH, `kubeconfig` e JSON de conta de serviço viram texto que a persona grava sozinha,
   sem permissão garantida e sem limpeza.

A entrevista de intent de 2026-10-23/24 (#54) decidiu o rumo: o cofre continua **para agentes**, com cifra no
servidor e entrega pelo despacho. Um cofre de senhas humano com cifra no cliente foi descartado, porque impede o
servidor de entregar o valor à run, que é o motivo de o cofre existir.

## Decisão

### 1. Segredo com campos
1. **Modelo.** `secrets` continua sendo o item (nome, descrição, escopo, dono). O valor sai do item e vai para
   `secret_fields` (migration 00203): `name` (`^[A-Za-z][A-Za-z0-9_-]{0,63}$`, único no segredo), `kind`, `sensitive`,
   `provider` (`local` | `vault`), `value_enc` ou `external_ref`, `version`, `revealed_at`, `last_used_at`,
   `position`. Não há tipos fixos de item: um item é o conjunto dos seus campos.
2. **Tipos de campo** (`kind`): `text` (valor de até 8 KiB), `totp` (semente; ver §4.3) e `file` (ver §5).
3. **Secreto × visível** (`sensitive`). Todo campo é **cifrado**, inclusive o visível. A diferença é quem lê: o campo
   visível (usuário, URL, região) aparece para `member` na tela e na API; o secreto segue o ADR-0050 §6 (não há leitura,
   só revelar uma vez por versão). A marca padrão é `sensitive = true`; `totp` e `file` são sempre secretos.
4. **Referência.** `${secret:ITEM#campo}` e `${secret:user/ITEM#campo}`. Sem `#campo`, vale o campo `value`:
   `${secret:X}` continua resolvendo como hoje. O `#` não pertence ao nome do segredo (padrão do ADR-0050 §A.1), então a
   gramática não é ambígua. O provider externo já usa a mesma forma (`caminho#campo`, ADR-0050 §13).
5. **Cifra.** AAD por campo: `secret/<secret_id>/field/<field_id>`. Copiar o texto cifrado de um campo para outro não
   decifra. `hanko secret rekey` passa a percorrer `secret_fields` e `secret_field_versions`.
6. **Provider por campo.** `provider` e `external_ref` passam do item para o campo: um item pode ter `username` local e
   `password` lido do cofre externo.
7. **Migração do que existe** (fatia 1, migration própria, depois do código que lê campos): cada segredo vira um item
   com o campo `value` (mesmo provider, mesmo `value_enc`/`external_ref`, mesma `version` e `revealed_at`). Para não
   precisar da chave na migration SQL, o campo migrado guarda `aad_version = 0`, que significa AAD legado
   `secret/<secret_id>`; o `rekey` seguinte o reescreve com `aad_version = 1`. As colunas `provider`, `value_enc` e
   `external_ref` de `secrets` deixam de ser lidas na fatia 1 e saem numa migration posterior, depois de um release.

### 2. Coleções
1. **Modelo.** `secret_collections` (nome único na empresa, descrição, `rotate_after_days` opcional) e
   `secret_collection_items` (um segredo da empresa pode estar em várias coleções). Segredo de usuário não entra em
   coleção (ver §6).
2. **Concessões** (`secret_collection_grants`):

   | Sujeito (`subject_type`) | Permissão | Efeito |
   |---|---|---|
   | `persona` | `use` | a persona resolve itens da coleção nas suas runs |
   | `persona_tree` | `use` | a persona e toda a cadeia abaixo dela no organograma (`reports_to`, organograma do M3 em `docs/ROADMAP.md`) |
   | `all_personas` | `use` | toda persona da empresa, inclusive as contratadas depois |
   | `user` | `view` | o humano vê nomes, campos visíveis e uso dos itens da coleção |
   | `user` | `manage` | o humano também cria, rotaciona, revela, restaura e apaga itens da coleção |

   **Desvio da decisão 3, para o Bardi aprovar:** a issue fala em conceder a "times". O Hanko não tem a entidade time:
   pacotes de time instalam personas soltas (`db/migrations/00132_pacotes_de_time.sql`, ADR-0044). Este ADR troca "time"
   por `persona_tree` (subárvore do organograma), que é o agrupamento que existe. Se um dia existir a entidade time, ela
   entra como novo `subject_type` sem mudar o resto.
3. **Papéis.** Admin da empresa (ADR-0021) continua gerenciando tudo e cria coleções e concessões. `manage` amplia o
   acesso de um member a uma coleção; `view` não dá leitura de campo secreto a ninguém.
4. **Onde a coleção vale.** A coleção decide o que chega **ao processo da persona**: `adapter_config` da persona, env
   de ambiente na run da persona (ADR-0037), env e headers de integração que o despacho entrega à run (ADR-0029),
   campos de plugin (ADR-0038), `hanko_secret_get` (§4) e `${secretfile:}` (§5). Usos **do servidor**, em que o valor
   nunca chega à persona, não passam por coleção: token de canal (ADR-0046), segredo HMAC de gatilho e de webhook
   (ADR-0047, ADR-0029), headers que o gateway de tools usa em nome da persona (ADR-0036), chave do decision model (ADR-0056) e
   credencial do mirror git (ADR-0054). Esses continuam resolvendo por `ResolveSecretText`, com a validação de hoje.
5. **Falha cedo.** Referência a item fora das coleções concedidas à persona falha a run antes do adapter, com o nome do
   item e da persona, nunca o valor (mesmo caminho do segredo inexistente, ADR-0050 §4). Na gravação da configuração, o
   aviso aparece (não bloqueia: a concessão pode vir depois).
6. **Migração.** A fatia 1 cria em cada empresa a coleção `padrão` (`is_default`), põe nela todos os segredos da empresa
   e a concede a `all_personas`. Nada que roda hoje quebra. Segredo criado depois entra na `padrão` só se quem cria
   não escolher outra coleção.

### 3. Histórico e restaurar
1. A cada rotação, o valor anterior do campo vai para `secret_field_versions` (cifrado com AAD
   `secret/<secret_id>/field/<field_id>/v/<version>`), com autor e instante.
2. **Retenção**: as últimas N versões **ou** as de até D dias, o que guardar menos. Padrão N = 5 e D = 90, nas chaves
   `secrets.history_keep_versions` e `secrets.history_keep_days` de `instance_settings` (ADR-0050 §C). Um job do river
   apaga o que passou do limite. O fato `secret.history_pruned` leva quantas versões saíram, nunca valor.
3. **Restaurar** (admin, ou `manage` na coleção; exige reautenticação recente, ADR-0070) copia a versão escolhida como
   **versão nova** do campo. Nada é apagado. Fato `secret.field_restored`.
4. Valor antigo segue "revelar uma vez por versão" (ADR-0050 §6): cada linha de histórico tem o seu `revealed_at`.

### 4. Segredo durante a run: `hanko_secret_get` e TOTP
1. **Tool MCP** `hanko_secret_get(item, campo?)`, no servidor `/mcp` e no gateway (ADR-0036/0053). Só resolve item de
   coleção concedida à persona da run (§2) ou item pessoal delegado à run (§6).
2. **Política.** A tool entra no motor off/ask/allow (ADR-0053) com padrão **`ask`** em toda empresa: a primeira chamada
   abre gate `kind=tool_call`; "sempre permitir" grava a regra por persona. Motivo do padrão: o valor devolvido entra
   no contexto do modelo, e daí pode chegar à memória, a artefato ou a comentário com outras palavras. A redação da run
   (`RedactingSink`) cobre o log e os eventos, não o que a persona reescreve.
3. **TOTP.** Campo `totp` guarda a semente (base32 ou URI `otpauth://`). `hanko_secret_get` devolve **só o código
   atual** (RFC 6238, SHA-1, 6 dígitos, passo de 30 s, salvo parâmetros da URI) e os segundos que faltam para vencer.
   A semente nunca sai: nem pela tool, nem por `${secret:}` (referência a campo `totp` no despacho é recusada na
   gravação, porque o código venceria antes do uso), nem por revelar via persona.
4. **Fatos.** Cada chamada grava uma linha em `secret_access_log` (item, campo, run, persona, via `tool`) e o fato
   `secret.accessed`, sem valor. O valor devolvido entra em `adapter.Spec.Secrets` da run em curso para a redação.

### 5. Campo arquivo e `${secretfile:}`
1. Campo `kind = file`: até **1 MiB**, cifrado com `secretbox` **antes** de ir ao `BlobStore` (ADR-0051), com o mesmo
   AAD do campo. O blob nunca existe em claro em nenhum driver (postgres, fs, s3). Guarda `file_name`, `content_type` e
   `size_bytes` como metadados visíveis.
2. **Referência** `${secretfile:ITEM#campo}` (e `user/`): no despacho vira o **caminho absoluto** de um arquivo criado
   com permissão `0600` num diretório `0700` do workspace da run, apagado ao fim da run, inclusive quando ela falha ou é
   cancelada. Ex.: `KUBECONFIG=${secretfile:prd-cluster#kubeconfig}`.
3. **Ambientes.** Adapter local e sandbox (ADR-0037) nesta entrega. Em ambiente SSH, `${secretfile:}` falha a run cedo
   com "arquivo de segredo ainda não é suportado em ambiente SSH". Suporte a SSH exige escrever o arquivo no host remoto
   e garantir a limpeza lá; fica para outro ADR.
4. `hanko_secret_get` devolve o conteúdo de campo arquivo só se for texto UTF-8; binário é recusado com o tamanho.

### 6. Segredo pessoal: delegação por run e saída do humano
1. **Delegação.** O dono de um segredo de usuário concede o uso a um trabalho **que ele mesmo pediu**:
   `secret_delegations` (dono, segredo, `target_type` = `task` | `conversation` | `run`, `target_id`, `expires_at`,
   `revoked_at`). O service confere que o dono é o autor da task ou o humano da conversa.
2. **Alcance.** `${secret:user/ITEM#campo}` e `hanko_secret_get` de item pessoal só resolvem numa run cujo alvo tem
   delegação válida do dono. A referência `user/` continua recusada na configuração compartilhada (persona,
   integração, ADR-0050 §3): a delegação é o único caminho.
3. **Desvio da decisão 11, para o Bardi aprovar:** a issue diz "só para aquela run". A run não existe quando o humano
   cria a task, e uma task pode gerar várias runs (retentativa, retomada, ADR-0039). Este ADR propõe: a delegação vale
   para as runs do alvo enquanto ele está aberto, até `expires_at` (padrão 24 h, máximo 7 dias) ou até o dono revogar.
   Uma delegação com `target_type = run` existe para o caso estrito (delegar a uma run já em curso).
4. **Relatório do dono.** Cada uso vira linha em `secret_access_log` com a delegação. Só o dono vê esse histórico
   (mesma regra do ADR-0050 §7).
5. **Saída do humano** (ADR-0057). Ao remover alguém da empresa, os segredos de usuário dele naquela empresa são
   apagados na mesma transação, as delegações caem junto e o fato `secret.user_purged` registra quantos itens e quantas
   delegações ativas havia, sem nomes. Não existe acesso de emergência.

### 7. Saúde dos segredos e gerador
1. **Gerador** configurável (tamanho 8–128, classes de caractere, frase-senha de 3–12 palavras de uma lista embutida),
   na API, na UI e no `generate` de criação/rotação. Sem parâmetros, continua gerando 32 bytes aleatórios (ADR-0050 §6).
2. **Relatório** por empresa, calculado sem rede:

   | Achado | Regra |
   |---|---|
   | repetido | dois campos com o mesmo `value_hmac` (HMAC-SHA256 com chave derivada de `HANKO_SECRET_KEY`); o relatório mostra os itens, nunca o valor |
   | velho | campo sem rotação há mais de `rotate_after_days` do item ou da coleção |
   | sem uso | `last_used_at` nulo ou mais antigo que 90 dias |
   | órfão | nenhuma persona, integração, ambiente, plugin, canal ou gatilho referencia o item |
   | fraco | campo marcado como senha (`name` = `password`/`senha` ou marca explícita) com entropia estimada < 60 bits |
3. **Alerta de rotação**: item que passa do prazo abre alerta `secret_rotation_due` (ADR-0020), um por item, fechado
   quando o campo é rotacionado.
4. **Fora:** consulta a serviço externo de vazamento (rede para fora e dependência nova, ADR-0011).

### 8. Import de `.env` e export cifrado
1. **Import de `.env`** (CLI `hanko secret import-env`, UI): prévia com o mapeamento (um item com N campos, ou N itens
   de um campo), conflitos de nome e coleção de destino; gravar exige reautenticação (ADR-0070). Aceita comentários,
   `export`, aspas simples e duplas e linhas vazias.
2. **Export cifrado** (`hanko secret export --out cofre.hkv`): arquivo com cabeçalho, sal e parâmetros; conteúdo
   cifrado com AES-256-GCM sob chave derivada por PBKDF2-SHA256 (600 000 iterações) da senha digitada na hora. Leva
   itens, campos (inclusive arquivo), coleções e concessões a `all_personas`; concessões a persona e usuário vão por slug
   e e-mail e só se aplicam se existirem no destino. Exige admin e reautenticação. O `hanko export` de empresa
   (ADR-0022) continua levando só referências.
3. **Import do export** grava tudo ou nada; senha errada falha sem gravar.

### 9. Linguagem (atualiza o ADR-0016)
| Código / API | UI pt-BR |
|---|---|
| `secret` | Segredo (o item) |
| `secret_field` | Campo |
| `vault` | Cofre (tela e menu com segredos, coleções, envios e saúde) |
| `collection` | Coleção |
| `send` | Envio (ADR-0069) |
| `delegation` | Delegação |

O pacote do cofre externo passa de `internal/vault` para `internal/extvault`, e o conceito se chama **provider externo**,
para que "vault" no código signifique só o cofre do Hanko. O valor `provider = 'vault'` no banco e na API **não muda**
(contrato em produção).

### 10. Contrato (fatia 0, #55)
- Migration `00203_cofre.sql`: tabelas novas e colunas novas, sem mover dado. A cópia dos segredos para campos e a
  coleção `padrão` vêm na migration da fatia 1, junto com o código que lê campos; antes disso, o código atual continua
  escrevendo em `secrets.value_enc`, e copiar agora deixaria os campos desatualizados.
- Tipos de domínio em `internal/domain/vault.go`.
- Queries em `db/queries/vault.sql`, `sends.sql` e `passkeys.sql`.
- Operações da API registradas em `internal/api/vault.go`, que respondem **501** até a fatia correspondente ligar o
  backend (padrão dos backends opcionais, como `secrets()` e `environments()`).

### 11. Modelo de ameaça (o que muda em relação ao ADR-0050 §11)
- **Ganha:** persona comprometida ou mal configurada alcança só as coleções concedidas; rotação ruim tem volta;
  arquivo de segredo tem permissão e limpeza garantidas; segredo pessoal só age com consentimento do dono, por alvo.
- **Perde:** o histórico guarda até N valores antigos por campo, então um backup roubado **com** a chave revela mais do
  que hoje. A retenção curta (5 versões ou 90 dias) é o limite desse custo.
- **Não muda:** quem tem `HANKO_SECRET_KEY` e o banco lê tudo; um admin que define a configuração de uma persona faz o
  agente usar o segredo. `hanko_secret_get` em `allow` põe o valor no contexto do modelo; por isso o padrão é `ask`.

## Alternativas descartadas
- **Cifra no cliente (zero-knowledge) no cofre.** O servidor deixaria de conseguir entregar o valor à run.
- **Tipos fixos de item** (login, cartão, identidade). Não há uso para agentes que os campos livres não cubram, e cada
  tipo exige um formulário próprio.
- **Agrupar campos só por convenção de nome** (`github.password`, `github.username`). Não permite TOTP, arquivo nem
  marca de visível sem casos especiais.
- **Lista de acesso por item.** Fica trabalhosa com muitos itens; a coleção agrupa.
- **Histórico sem limite.** Aumenta o que vaza num backup roubado sem ajudar o caso real (desfazer uma rotação recente).
- **Copiar segredos para campos já na migration do contrato.** Ver §10.

## Consequências
- `secretref` ganha `#campo` e `${secretfile:}` na fatia 1 e 3. Até lá, nenhum comportamento muda.
- Novas tabelas com `company_id` em toda linha (ADR-0010); toda escrita gera `activity_event` na mesma transação.
- Fatos novos: `secret.field_created|field_updated|field_deleted|field_restored|history_pruned|accessed|user_purged`,
  `collection.created|updated|deleted|item_added|item_removed|granted|revoked`, `delegation.created|revoked|expired`.
- Testes de não-vazamento com valor sentinela (padrão do ADR-0050 §10) passam a cobrir campos, histórico, arquivo,
  `hanko_secret_get`, relatório de saúde e export cifrado.
- O ADR-0050 continua valendo em tudo que este não altera.

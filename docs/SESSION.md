# Estado da última sessão (cofre, fatia 6, #61)
Data: 2026-10-24 · branch `feat/61-cofre-saude` · issue #61 (parte de #54)

## Feito
- Gerador configurável (tamanho 8–128, classes, frase-senha 3–12 palavras de lista embutida) na API, na criação e troca de valor e na UI; sem opções, o padrão de sempre.
- Relatório de saúde (`vault/health`, só admin, sem rede): repetido por HMAC sem decifrar, velho, sem uso, órfão (usos de persona, integração, ambiente, plugin, canal, gatilho, decisão e git), fraco (campo `password`/`senha` abaixo de 60 bits). Tela em Segredos › Saúde.
- Alerta `secret_rotation_due` (ADR-0020), um por segredo, fecha ao rotacionar. PATCH de descrição e prazo do item.

## Travado
- Nada. Fora do escopo: "marca explícita" de senha (ADR-0068 §7.2); hoje vale só o nome do campo.

## Próximo passo
- Depois do deploy, `hanko secret rekey` uma vez: preenche o HMAC dos campos antigos (sem ele, repetidos entre campos anteriores não aparecem).
- QA: demo do critério `qa` da #61 (repetido, órfão e velho no relatório; alerta dispara).

# Estado da última sessão (envio por link, fatia 4 do cofre, #59)
Data: 2026-10-24 · branch `sessao/59-cofre-envio-link` · issue #59 (parte de #54)

## Feito
- Envio de texto por link `/s/<id>#<chave>` (ADR-0069): criação por humano pela API com reautenticação, criação por persona pelo service (`item#campo` de coleção concedida), página de recebimento com WebCrypto, senha com prova e bloqueio, expiração por prazo e por acessos, job `send_purge_tick`.
- Testes: chave ausente de banco, evento e log (humano e persona), prazo, limite de acessos, senha errada, coleção concedida, reautenticação, interoperabilidade com o WebCrypto no Node, API pública sem credencial e cabeçalhos da página.

## Travado
- Envio de arquivo, listar/consultar/apagar envio e a tool `hanko_send_create` (precisa da política `ask` padrão antes de publicar) ficam em 501 ou fora. Tela e CLI de criação também.

## Próximo passo
- Tool MCP `hanko_send_create` com política `ask` padrão; depois upload do blob de arquivo e a rota de download de uso único; depois tela e CLI de criação.

# Estado da última sessão (cofre, fatia 1, #56)
Data: 2026-10-24 · branch `feat/56-cofre-campos` · issue #56 (parte de #54)

## Feito
- Campos de segredo (N por item, visível ou secreto), histórico por campo com retenção (5 versões ou 90 dias) e restaurar como versão nova, coleções com concessões, migração 00204.
- API (campos e coleções), CLI `hanko secret field|collection`, UI de campos e coleções, job `secret_history_tick`.
- Testes: service (critérios de build), API de integração, migração, UI e CLI.

## Travado
- Revelar campo, histórico, restaurar e conceder ou revogar concessão exigem reautenticação (ADR-0070, #57): seguem em 501 na API.

## Próximo passo
- Fatia 2 (#57): fechar a reautenticação e ligar essas 7 operações da API ao service já pronto.

# Estado da última sessão (shard `resto` do CI, #75)
Data: 2026-10-24 · branch `chore/75-resto-compila-paralelo` · issue #75

## Feito
- Job `test` do CI: um passo compila os testes em paralelo antes da rodada com `-p 1`, e `GORACE=atexit_sleep_ms=0` tira a espera de 1 s na saída de cada binário com `-race`. Nenhum teste nem gate muda.
- Medido localmente no `resto`: compilação a frio 226 s → 102 s; rodada completa 343 s → 268 s.
## Travado
- Merge do PR da #75 (só o dono). Seguem abertas as decisões da sessão anterior (#57, cofre): nome `reauth.minutes` e fato por empresa.
## Próximo passo
- Medir `test (resto)` em 3 execuções do CI (meta ≤ ~8 min). Se ainda for o caminho crítico: shard próprio para o `internal/heartbeat` (104 s locais), o que muda a decisão de dois shards fixos da #51.
>
# Estado da última sessão (cofre, fatia 3, #58)
Data: 2026-10-24 · branch `feat/58-cofre-tool-get` · issue #58 (parte de #54)

## Feito
- Tool `hanko_secret_get(item, campo)` com política (padrão `ask`, gate, "sempre permitir"), campo TOTP que devolve só o código, campo arquivo cifrado (migração 00205) e `${secretfile:ITEM#campo}` como arquivo 0600 apagado ao fim da run, inclusive na falha. Valor entregue pela tool entra na redação da run.
- Testes: serviço (critérios de build), heartbeat (integração), MCP, API e unitários de `secretref`, `totp` e `domain`.

## Travado
- `${secretfile:}` recusado em SSH, sandbox e cloud nesta entrega (sandbox monta o diretório de trabalho; o caminho do host não existe dentro do contêiner).
- Campo arquivo só pela API (`content_base64`); CLI `hanko secret field` ainda não aceita arquivo.

## Próximo passo
- Decidir o `${secretfile:}` para sandbox (bind mount do arquivo) e SSH (ADR próprio, ADR-0068 §5.3).

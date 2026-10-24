# ADR-0069 — Envio: link público com prazo e chave no fragmento
Data: 2026-10-24 · Status: aceita (2026-10-24) · Issue: #54 (decisões 6 e 7), contrato em #55, entrega em #59

## Contexto
Hoje, para passar uma credencial a alguém de fora (ou de uma persona a um humano), o valor vai por comentário, chat ou
canal externo (ADR-0046), e fica gravado lá. O Hanko não tem um jeito de entregar um valor uma vez, com prazo, sem
deixar cópia.

O envio é o único lugar em que um valor do cofre **sai** do Hanko para quem não tem conta. Isso pede um modelo de
ameaça próprio, separado do cofre (ADR-0068), em que o servidor precisa ler o valor para entregá-lo à run.

## Decisão
1. **O que é.** Um envio tem conteúdo (texto de até 64 KiB ou arquivo de até 25 MiB), prazo (`expires_at`, obrigatório,
   máximo 30 dias, padrão 7), limite de acessos opcional (1–1000) e senha opcional. Fica em `secret_sends`
   (migration 00203), com `company_id`.
2. **Link.** `https://<host>/s/<public_id>#<chave>`. `public_id` são 16 bytes aleatórios em base64url, separado do
   `id` (UUIDv7 é ordenado no tempo e não serve de segredo). A **chave** (32 bytes aleatórios, base64url) vai só no
   fragmento depois de `#`, que o navegador não envia ao servidor: não chega ao nginx, ao log, ao span OTel nem ao banco.
3. **Cifra.** O conteúdo é cifrado com AES-256-GCM sob uma chave derivada por HKDF-SHA256 da chave do link (e da senha,
   item 5). O servidor guarda só o texto cifrado (`ciphertext` para texto; blob cifrado no `BlobStore`, ADR-0051, para
   arquivo). O nome e o tipo do arquivo vão **dentro** do conteúdo cifrado.
4. **Quem cria.**
   - **Humano** (UI e CLI): o navegador ou a CLI gera a chave, cifra e envia só o texto cifrado. Exige reautenticação
     recente (ADR-0070) e papel `member` na empresa.
   - **Persona**: tool MCP `hanko_send_create(item, campo, expira_em, max_acessos?)`, sob a política de tools
     (ADR-0053) com padrão **`ask`**. A persona aponta para um campo do cofre que alcança (coleção concedida ou
     delegação, ADR-0068 §2 e §6) e **não digita o valor**: o servidor lê o campo, gera a chave, cifra, monta o link,
     devolve o link à persona e descarta a chave. Nesse caso o servidor vê o valor por um instante, como já vê no
     despacho; a garantia de não guardar a chave vale do mesmo jeito.
   - Texto livre digitado pela persona não vira envio: o valor teria passado pelo contexto do modelo.
5. **Senha opcional.** Derivada **no cliente** com PBKDF2-SHA256 (600 000 iterações, sal de 16 bytes guardado no
   envio) e misturada à chave do link no HKDF. Para contar acesso só depois da senha certa, o cliente manda uma prova
   (`HKDF(senha_derivada, "hanko-send-auth")`), e o servidor guarda só o SHA-256 dela (`password_verifier`). Senha
   errada não conta acesso e não devolve o texto cifrado. Cinco erros seguidos bloqueiam o envio por 15 minutos.
6. **Abrir.** Página pública `/s/<public_id>` (sem conta, sem cookie de sessão, `Referrer-Policy: no-referrer`,
   `Cache-Control: no-store`, CSP sem script de terceiros). A página pede a senha se houver, chama
   `POST /api/v1/s/<public_id>/open` com a prova, recebe o texto cifrado e decifra com WebCrypto. A chamada conta o
   acesso de forma atômica (`UPDATE … WHERE access_count < max_accesses AND expires_at > now()`). A CLI abre o mesmo link.
7. **Fim.** No limite de acessos ou no prazo, o conteúdo (texto cifrado e blob) é apagado e a linha fica como registro,
   com `purged_at`. O criador (ou admin) pode apagar antes. Um job do river varre os vencidos.
8. **Rate limit.** `/s/*` passa pelo token bucket por IP (ADR-0058) com limite próprio.
9. **Fatos.** `send.created|opened|expired|deleted`, com criador, tipo, prazo, limite e, para persona, item e campo de
   origem. Nunca chave, senha, prova ou conteúdo. O acesso público registra só contagem, instante e um hash truncado do
   IP.

## Modelo de ameaça
- **Banco ou backup vazado** (com ou sem `HANKO_SECRET_KEY`): não lê envio nenhum. Falta a chave do link.
- **Link vazado**: quem tem o link abre o envio, até o prazo ou o limite. A senha opcional é o segundo fator para esse
  caso.
- **Banco vazado + link vazado**: lê, como qualquer link vazado.
- **Servidor comprometido ativamente**: pode servir uma página de recebimento alterada que exfiltra a chave. Isso vale
  para qualquer cifra feita em página web servida pelo mesmo servidor; a CLI não tem esse risco.
- **Envio criado por persona**: o servidor viu o valor ao cifrar. A proteção contra banco vazado continua valendo,
  porque a chave não é guardada.

## Alternativas descartadas
- **Cifra no servidor com `secretbox`.** Um banco vazado junto com a chave revelaria todos os envios ativos, e o envio
  é justamente o valor que sai do Hanko.
- **Persona cria envio de texto livre.** O valor passaria pelo contexto do modelo.
- **Envio sem prazo.** Link eterno vira canal permanente de vazamento.

## Consequências
- Primeira página pública sem sessão no host, além da tela de login. Ganha CSP própria e teste de que não carrega script
  externo.
- Página de recebimento usa WebCrypto (PBKDF2, HKDF, AES-GCM) do navegador, sem dependência nova.
- Teste com valor sentinela confere que chave, senha e conteúdo em claro não aparecem em log, span, evento, banco nem
  resposta da API.

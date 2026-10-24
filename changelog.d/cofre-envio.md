### Cofre para agentes: envio por link com a chave no fragmento (fatia 4, #59)

- Envio de texto por link `/s/<id>#<chave>`: o servidor guarda só o texto cifrado (AES-256-GCM). A chave vai só no fragmento, que o navegador não envia; não aparece no banco, nos eventos nem no log.
- Criação por humano pela API (exige reautenticação recente): o navegador ou a CLI gera a chave, cifra e envia só o texto cifrado.
- Criação por persona pelo service: `item#campo` de coleção concedida à persona; o servidor cifra, monta o link e descarta a chave. A tool MCP `hanko_send_create` (política `ask`) fica para a entrega seguinte.
- Página de recebimento `/s/<id>`, sem conta: pede a senha quando há, abre pela API pública e decifra no navegador com WebCrypto. Senha errada não devolve o texto e, depois de 5 erros, bloqueia o envio por 15 minutos.
- Expira por prazo e por limite de acessos; o último acesso já apaga o conteúdo, e o job `send_purge_tick` (a cada 5 min) apaga o que ninguém abriu.
- Ainda não entram nesta entrega: envio de arquivo (upload do blob), listar, consultar e apagar envios, a tool MCP da persona, a tela e o comando da CLI de criação, e o limite de taxa da rota pública (ADR-0058).
- English summary: secure send by link (ADR-0069, #59): text ciphertext only on the server, key in the URL fragment, human creation via API with recent re-authentication, persona creation from a granted `item#field` (server encrypts and drops the key), password proof with lock after 5 failures, expiry by time and access count with purge, and a public page that decrypts in the browser with WebCrypto. File sends, list/get/delete, the persona MCP tool, the UI/CLI creation and the public-route rate limit are still pending.

### Cofre para agentes: envio com link, gestão e tool da persona (fatia 4, #59)

- Envio de arquivo: `POST /companies/{company}/sends` aceita `kind: file` com o tamanho do arquivo (até 25 MiB), sem conteúdo; o conteúdo cifrado sobe em `PUT /companies/{company}/sends/{id}/content`, só de quem criou o envio, com o tamanho declarado mais a cifra. Subir de novo troca o conteúdo.
- Gestão do envio: `GET /companies/{company}/sends` (admin vê todos; membro, só os que criou), `GET …/sends/{id}` e `DELETE …/sends/{id}` (apaga o conteúdo antes do prazo, pelo criador ou pelo admin). O blob sai junto com o conteúdo, no prazo, no limite de acessos ou no pedido.
- Envio de arquivo ainda não abre pelo link: a página recebe recusa clara, sem contar acesso. A entrega do arquivo para a página fica para a fatia seguinte.
- Tool MCP `hanko_send_create(item, campo?, expira_em?, max_acessos?)`: a persona cria um envio de um campo que alcança e recebe o link, sem digitar o valor. Política `ask` por padrão, como `hanko_secret_get`; pedido inválido nem abre aprovação. O link entra na redação do log da run.
- English summary: encrypted file sends (up to 25 MiB, uploaded after creation by the sender only), send list, read and delete by role (admin sees all, members their own), and the persona MCP tool `hanko_send_create` with an `ask` default policy. File sends do not open through the public link yet.

### Assistente "Conectar ferramenta a uma persona"

- Um fluxo só liga um servidor MCP a personas: servidor (header secreto ou OAuth 2.1 com PKCE), descoberta das tools por risco, escolha das personas, modelo de acesso e prévia do que cada persona poderá fazer antes de gravar (ADR-0089).
- Modelos de acesso por empresa: "Somente leitura" e "Leitura livre, escrita com aprovação"; a escrita da conexão nova só sai da quarentena pelo modelo escolhido.
- Comando `hanko integration connect`; a gravação exige `--yes` depois da prévia.
- English summary: guided assistant that connects an MCP server to personas with an access template (read-only or read-free with write approval), in the web UI and as `hanko integration connect` (ADR-0089).

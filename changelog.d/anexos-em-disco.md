### A persona enxerga imagens e PDFs anexados (ADR-0066)
- Antes de cada run, os anexos da task ou da conversa são gravados no diretório de trabalho, em `.hanko-anexos/<run>/`, e o briefing dá o caminho de cada um; a persona abre com a ferramenta de leitura de arquivos do harness (o Read do claude lê imagem e PDF). Antes, binário só vinha pelo MCP em base64, até 1 MiB, e o modelo não o enxergava.
- A pasta tem `.gitignore`, some no fim da run e tem teto de 512 MiB por run; o que não couber segue pelo `hanko_attachment_get`.
- English summary: run attachments are written to `.hanko-anexos/<run>/` in the work dir and the briefing gives each path, so personas can open images and PDFs with their file tools instead of unreadable base64.

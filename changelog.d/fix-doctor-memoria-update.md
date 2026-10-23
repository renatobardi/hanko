### Correções do `hanko doctor` e do `hanko update --check`

- `hanko doctor`: a checagem do ai-memory usa o mesmo teste do servidor (o `/health`, se a imagem tiver, senão a tool MCP `memory_status`). A imagem atual não expõe `/health` e o doctor avisava "HTTP 404" com a memória saudável.
- `hanko update --check` num build de commit (como o do `deploy-prd.sh`) não anuncia mais "versão nova" quando a release do canal é anterior; diz que é build de desenvolvimento e mostra a versão do canal. O JSON ganha `dev`.
- English summary: `hanko doctor` checks ai-memory the way the server does (falls back to the `memory_status` MCP tool when the image has no `/health`), and `hanko update --check` no longer suggests a "newer" stable release to a commit build.
- Release: o cask do Homebrew usa `hooks.post.install_steps` (passos declarativos) no lugar do `install` em Ruby, depreciado no goreleaser v2.19, que reprovava o `goreleaser check` do CI.

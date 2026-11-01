### ai-memory 3.3.1

- O digest fixado do ai-memory passa a ser o da 3.3.1, no `docker-compose.yml`, no `.env.example` e no primeiro deploy de `deploy-prd.sh`, que antes usava `latest`. Uma instalação existente segue com o `AI_MEMORY_IMAGE` do seu `.env`.
- O diff das propostas de curadoria mostra o texto da página, não o JSON da resposta, e pede o teto de 64.000 caracteres: a 3.3 corta em 12.000 sem esse pedido, e o resto da página pareceria removido. Página maior que o teto vem com aviso de corte.
- As tools novas `memory_patch_page` e `memory_restore_page` entram na política de curadoria como retidas, e a proposta de um patch mostra as edições.
- `deploy/compose.yaml` define `AI_MEMORY_ALLOWED_HOSTS`. Sem isso o servidor recusa o Hanko com 403.

- English summary: the pinned ai-memory digest is now 3.3.1 (`docker-compose.yml`, `.env.example`, first `deploy-prd.sh` run, which used `latest`); existing installs keep their `.env` value. Curation diffs show the page text and request the 64,000-character ceiling, because 3.3 cuts at 12,000 by default. `memory_patch_page` and `memory_restore_page` are held for human review, and a patch proposal shows its edits. `deploy/compose.yaml` sets `AI_MEMORY_ALLOWED_HOSTS`, without which the server answers 403 to Hanko.

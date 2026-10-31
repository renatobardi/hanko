# Deploy com imagem construída fora do servidor · sessão `estudo-issues`
Data: 2026-10-31

## Feito
- `deploy-prd.sh --build-local`: constrói a imagem no Mac (arquitetura do servidor) e a envia ao `hanko-prd` com `docker save | ssh | docker load`; sobe com `compose up --no-build`. Sem a opção, nada muda.
- Causa do problema: o deploy da v0.8.0 compilou o Go dentro do LXC (teto de 4 GiB) e levou o IO do host a 99% de pressão; o build morreu sem imagem nova.

## Travado
- Não testei o script: este ambiente não alcança o oute-server. Só `bash -n`. O primeiro uso real é o deploy da v0.8.0.

## Próximo passo
- Bardi: merge deste PR, depois o deploy da v0.8.0 com `--build-local` (pedido pelo canal). A produção segue na v0.7.2.

# Estado da última sessão (compositor: colar, miniaturas, Enter, caixa fixa e PDF grande)
Data: 2026-10-23 · branch `feat/compositor-colar-miniaturas` → `main`

## Feito
- Colar imagem/arquivo anexa e mostra a miniatura; anexos viram miniaturas (nome ao passar o mouse); X remove da prévia.
- Enter envia, Shift+Enter quebra a linha (celular: Enter quebra); caixa fixa embaixo com alfinete e setas início/fim.
- PDF grande: texto extraído + partes de 20 páginas antes da run (poppler-utils e qpdf na imagem); adendo no ADR-0066.
## Travado (só o dono)
- Deploy pelo Mac (`deploy/oute-server/deploy-prd.sh`, sem `--edge`: o nginx não mudou); a imagem muda (apt: poppler-utils, qpdf), então o build demora mais.
## Próximo passo
- Depois do deploy: reenviar o PDF de 23 MiB e pedir à persona um resumo; colar um print no chat e conferir a miniatura.

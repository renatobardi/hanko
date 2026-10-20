# Marca — card de prévia social

`social-card.html` é a fonte do card de prévia social do repositório (1280×640, PNG < 1 MB),
com os tokens do Kubo DS (Inter, pedra quente, anel em vez de borda) e o selo sakura.
Tudo que importa fica dentro da margem de segurança de 80px (ver `social-card-guides.png`).

| Arquivo | Uso |
|---|---|
| `social-card.png` | padrão (EN, claro) — o que vai nas configurações do repositório |
| `social-card-dark.png` | EN, escuro |
| `social-card-pt.png` / `social-card-pt-dark.png` | PT-BR, claro / escuro |
| `social-card-guides.png` | conferência da margem de segurança (não publicar) |

Regerar depois de mexer no HTML ou nos tokens:

```sh
node scripts/gen-social-card.mjs
# playwright instalado globalmente:
NODE_PATH="$(npm root -g)" node scripts/gen-social-card.mjs
```

Publicar: Settings → General → Social preview → enviar `docs/brand/social-card.png`.

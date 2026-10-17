[English](SECURITY.md) | **Português**

# Política de segurança

## Como reportar uma vulnerabilidade

**Não** abra issue pública para problema de segurança.

Reporte em privado pelo GitHub: **Security → Report a vulnerability** em
<https://github.com/renatobardi/hanko/security/advisories/new> (relato privado de vulnerabilidade).

Inclua o que puder: versão ou commit afetado, como reproduzir, o impacto que você enxerga e se exige usuário
autenticado. Não envie credenciais, tokens ou dados de clientes reais.

O que esperar:

- confirmação de recebimento em até **3 dias úteis**;
- uma primeira avaliação (aceito, precisa de mais informação ou não é vulnerabilidade) em até **7 dias**;
- correção e data de divulgação combinada com você; crédito nas notas da versão, se quiser.

## Versões suportadas

O Hanko ainda é pré-1.0. As correções vão para a última versão estável (a tag `vX.Y.Z` mais recente) e para o `main`
(canal `edge`). Versões antigas não recebem backport; atualize para a última estável.
Os canais estão descritos em [docs/RELEASING.md](docs/RELEASING.md).

## Escopo

Dentro: o binário `hanko` e sua API HTTP, UI, CLI e servidor MCP; autenticação e sessões; isolamento entre empresas
(toda leitura e escrita é restrita à empresa); tratamento de segredos (segredos de integração ficam cifrados em
repouso e nunca são devolvidos); os caminhos de entrega de artefatos e webhooks; as imagens de container e os
artefatos de release.

Fora: vulnerabilidades em agentes (harnesses) de terceiros que o Hanko apenas dispara; achados que exigem o modo
`trusted` numa rede exposta (documentado como só local, ver ADR-0017/0018); negação de serviço por volume contra
uma instalação sem proxy reverso.

## Como mantemos as dependências seguras

- `govulncheck` e `trivy fs` rodam em todo pull request, no `main` e toda semana (`.github/workflows/security.yml`).
- O Dependabot abre atualizações semanais para módulos Go, a ferramenta de build das ilhas React (npm) e GitHub
  Actions (`.github/dependabot.yml`).
- O Postgres é a única dependência de runtime; dependência Go nova exige linha justificada em ADR.

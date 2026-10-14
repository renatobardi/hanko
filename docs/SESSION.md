# SESSION

- Correções de M3/M4 na branch `fix/m3m4` (migration 00006): reserva de orçamento por run aberta (`HANKO_RUN_RESERVE_USD`), run.cancel ao estourar, pausa com `persona.paused` e retomada automática (teto sobe, mês vira).
- Preço de reserva (prefixo vazio) para modelo sem preço + `cost.unknown_model`; `wake_requests.seq` evita wake perdido por coalescência; tick tem rede de segurança (task mudada há >1 min sem run).
- Gates: persona só pede na task própria/da sua run; wake vai ao dono atual; `task_review` cobre `cancelled`; task encerrada expira gates pendentes.
- Persona só altera task própria, da subárvore ou sem dono. Fluxos: manual não colide com o agendado, não empilha task aberta (`flow.skipped`), após downtime dispara só a mais recente (`flow.missed`).
- Desvios: sobreposição de fluxo ignora tasks do "Rodar agora"; a rede de segurança exige que nenhuma run tenha sido aberta depois da última mudança (evita laço de runs).
- Próximo: M6.

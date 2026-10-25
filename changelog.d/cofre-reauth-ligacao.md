### Cofre para agentes: ações sensíveis da fatia 1 ligadas à reautenticação (#56)

- Doze operações do contrato (fatia 0, #55) saem do 501 e passam a responder: revelar campo, listar histórico, revelar versão anterior, restaurar versão e apagar campo, em `secrets` e em `user-secrets`; criar e revogar concessão de coleção em `secret-collections/{coleção}/grants`.
- Todas são ações sensíveis (ADR-0070 §1) e exigem reautenticação recente no service: sessão sem prova recebe `401 reauth_required` com a janela; API key de escopo `full` e modo trusted passam (ADR-0070 §5).
- Revelar segue uma vez por versão (campo e versão do histórico); restaurar cria versão nova; apagar campo leva o histórico junto e o campo `value` não se apaga.
- Com isso, uma concessão de coleção nova pode ser criada por HTTP: até esta versão, as personas só alcançavam segredos pela coleção `padrão` (concedida a todas).
- Sem migration.
- English summary: twelve contract operations now respond instead of 501 — reveal field, list history, reveal and restore a previous version, and delete a field (for both company and personal secrets), plus creating and revoking a collection grant. All are sensitive actions under ADR-0070 and require recent re-authentication in the service (401 `reauth_required` without it; full-scope API keys and trusted mode pass). Reveal stays once per version, restore still creates a new version, and deleting a field drops its history while the `value` field cannot be deleted. No migration.

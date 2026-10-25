### Cofre: marca de senha em campo (fatia 6, #61)

- Campo pode ser marcado como senha (ADR-0074): entra no achado "fraco" do relatório de saúde mesmo com outro nome (`chave`, `pin`). Vale só para campo texto secreto. Marca via `POST …/fields`, `PATCH …/fields/{field}` e `hanko secret field add|set` (`--password`, `--no-password`).
- Campos `password` e `senha` continuam no relatório pelo nome, como antes.
- English summary: a vault field can be marked as a password (ADR-0074). Marked fields join the weak-password finding of the health report even with another name. Only secret text fields can be marked. Set with `POST …/fields`, `PATCH …/fields/{field}` or `hanko secret field add|set` (`--password`, `--no-password`).

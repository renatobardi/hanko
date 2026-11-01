### Proxy de memória mais estrito

- O proxy `/memory-mcp` fixa o escopo da run em toda chamada e só aceita as tools e os argumentos revisados. O resto é recusado e fica no log, só com os nomes.
- A memória da persona segue o workspace da run, na ordem task, projeto, persona. Antes, uma task que herdava o workspace do projeto podia usar a memória de outro lugar.
- Personas passam a alcançar outro workspace só por concessão (`hanko memory grant`), e a busca delas não traz a área global do ai-memory.

- English summary: the `/memory-mcp` proxy pins the run's scope on every call and only accepts reviewed tools and arguments; anything else is refused and logged by name. Persona memory follows the run's workspace (task, project, persona). Personas reach another workspace only through a grant, and their search no longer returns ai-memory's global area.

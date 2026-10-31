### Gerador único de nome

- O Hanko ganha o gerador único de nome (ADR-0084), sobre as listas de 256 + 256 palavras aprovadas: o nome derivado do id (`ExternalName`, com 16 variações e, no fim, o sufixo de 3 dígitos), a escolha do nome livre da empresa (`ChooseExternalName`) e o sorteio entre os livres (`DrawExternalName`). A conversa externa e a rodada externa passam a poder ter nome; a ligação delas ao gerador vem no PR seguinte da fatia.

- English summary: Hanko gains the single name generator (ADR-0084) over the approved 256 + 256 word lists: the name derived from the id (`ExternalName`, with 16 variations and, last, the 3-digit suffix), picking a name the company has free (`ChooseExternalName`) and drawing among the free ones (`DrawExternalName`). External conversations and rounds can now have names; wiring them to the generator comes in the slice's next PR.

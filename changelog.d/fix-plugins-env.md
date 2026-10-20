### Correção — `HANKO_PLUGINS_DIR` e `HANKO_PLUGINS_DATA_DIR` agora são lidas do ambiente
- O carregador de config ignorava essas duas variáveis (só valiam no YAML), então os plugins de terceiros nunca ligavam por ambiente. Teste novo garante que toda tag `env` de `Config` é lida.
- English summary: `HANKO_PLUGINS_DIR`/`HANKO_PLUGINS_DATA_DIR` were silently ignored when set via environment; fixed, with a guard test covering every `env` tag.

### Correção — CI: testes unitários em série
- O passo `test` rodava os pacotes em paralelo contra o mesmo banco de teste, e `testdb` recria o schema a cada teste; com mais pacotes usando banco, o passo passou a falhar de forma intermitente. Agora usa `-p 1`, como o passo de integração.

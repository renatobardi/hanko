### Cofre: exportar e importar (.hkv) e importar .env aceitam prova por Google e passkey (#92)

- Quando o servidor pede prova recente, as telas de exportar e importar o cofre cifrado (`.hkv`) e a de importar `.env` oferecem "Confirmar identidade": o mesmo diálogo do perfil (senha, passkey ou Google). Depois da prova, envie o formulário de novo sem a senha da conta (ADR-0070 §3). Antes, só a senha da conta valia nessas telas.
- English summary: the encrypted-vault (`.hkv`) export and import screens and the `.env` import screen now accept a passkey or Google (Firebase) proof, through the profile's proof dialog, besides the account password.

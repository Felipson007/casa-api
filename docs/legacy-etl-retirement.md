# Aposentadoria do ETL legado

Revisado em 2026-08-28.

O placeholder `cmd/migrate-legacy` foi removido. A reescrita já opera sobre o schema novo, as
migrations são aplicadas automaticamente no boot e não há banco Java declarado como fonte ativa de
importação. Manter um binário que apenas terminava com `TODO` sugeria incorretamente que uma etapa
operacional ainda era necessária.

Os mapas históricos em `architecture-plan.md` e `schema-migration-map.md` continuam úteis como
registro das decisões. Se uma nova importação for necessária, ela deve nascer como tarefa própria,
com fonte, destino, estratégia de idempotência, backup e ensaio em cópia do banco explicitamente
definidos; não deve ressuscitar silenciosamente o comando antigo.

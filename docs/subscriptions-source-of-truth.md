# Assinaturas: fonte canônica

Revisado em 2026-08-29.

A fonte operacional de assinaturas é `expenses` com `recurring=true`, exposta por
`GET /api/expenses/subscriptions`. O flag `recurring` é só um pré-filtro grosseiro; o que define
uma assinatura é `looksLikeSubscription` em `internal/expenses/reports.go`, que exige **todas** as
condições abaixo (parâmetro `now` = data de referência):

- **≥ 3 meses distintos** de cobrança e **≥ 3 lançamentos**;
- **meses consecutivos** — sem buraco entre o primeiro e o último mês (Fev/Mar/Abr sim; Fev/Abr/Ago
  não, isso é comerciante esporádico);
- **valor estável** — a cobrança mais cara ≤ 1,2× a mais barata (tolera um reajuste real, rejeita a
  variação de iFood/pet shop);
- **≈ 1 cobrança por mês** (folga de +1 para rateio/plano anual junto de um mensal);
- **ainda ativa** — a cobrança mais recente não pode estar mais de 1 mês de competência atrás de
  `now`; uma assinatura que parou de cobrar não é assinatura corrente (era o bug do "iFood -
  Gabriel", listado em agosto com próxima cobrança em maio).

A tabela `subscriptions`, criada pela migration `000010`, não é usada pela aplicação e permanece
apenas por compatibilidade de schema. Nenhum código novo deve escrever nela. Removê-la exige uma
migration destrutiva separada, depois de confirmar que não há dados reais.

O endpoint devolve a cobrança mais recente e uma próxima data estimada: a primeira mensalidade
(mesmo dia do mês, limitado ao último dia quando necessário) que **não** esteja antes de `now` —
`nextChargeOnOrAfter` avança mês a mês até passar de `now`, então a estimativa nunca cai no
passado. “Plano” não é inferido porque faturas de gastos não trazem esse dado de forma confiável.

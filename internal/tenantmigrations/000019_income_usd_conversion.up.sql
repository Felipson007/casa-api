-- ============================================================================================
-- income_usd_conversion — a person paid in USD by a foreign employer (converted to BRL monthly,
-- e.g. via Wise) needs both the USD amount actually received and the exchange rate used to convert
-- it, alongside the existing BRL `amount` (which stays the source of truth for every total/report —
-- these two columns are purely informational). Both nullable: only meaningful together, and only
-- for the subset of income entries that came from a foreign-currency payment.
-- ============================================================================================
ALTER TABLE income_entries
    ADD COLUMN usd_amount numeric(15,2),
    ADD COLUMN exchange_rate numeric(10,4);

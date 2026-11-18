-- ============================================================================================
-- job_experiences_contract_currency — adds contract type (CLT/PJ, needed to know whether payroll
-- tax withholding — INSS/IRRF — even applies) and the same optional USD/exchange-rate pair
-- income_entries already has (migration 000019): salary (BRL) stays the source of truth, these two
-- are purely informational for a foreign-currency-paid position.
-- ============================================================================================
ALTER TABLE job_experiences
    ADD COLUMN contract_type varchar(10) NOT NULL DEFAULT 'CLT',
    ADD COLUMN usd_amount numeric(15,2),
    ADD COLUMN exchange_rate numeric(10,4);

ALTER TABLE job_experiences
    ADD CONSTRAINT ck_job_experiences_contract_type CHECK (contract_type IN ('CLT', 'PJ'));

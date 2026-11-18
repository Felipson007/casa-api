DROP TRIGGER IF EXISTS trg_income_bank_projection_deleted ON income_entries;
DROP TRIGGER IF EXISTS trg_expenses_bank_projection_deleted ON expenses;
DROP FUNCTION IF EXISTS bank_projection_deleted();

DELETE FROM external_integrations WHERE type = 'PLUGGY';
ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER'));

DROP INDEX IF EXISTS uq_financial_balances_bank_account_id;
ALTER TABLE financial_balances DROP COLUMN IF EXISTS bank_account_id;

DROP INDEX IF EXISTS uq_income_entries_bank_transaction_id;
ALTER TABLE income_entries DROP COLUMN IF EXISTS bank_transaction_id, DROP COLUMN IF EXISTS source;

DROP INDEX IF EXISTS uq_expenses_bank_transaction_id;
ALTER TABLE expenses DROP COLUMN IF EXISTS bank_transaction_id, DROP COLUMN IF EXISTS source;

DROP TABLE IF EXISTS investment_snapshots;
DROP TABLE IF EXISTS investment_positions;
DROP TABLE IF EXISTS bank_transactions;
DROP TABLE IF EXISTS credit_card_bills;
DROP TABLE IF EXISTS bank_accounts;
DROP TABLE IF EXISTS bank_connections;

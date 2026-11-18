-- ============================================================================================
-- Open Finance (Pluggy) — automatic bank/card/investment sync. Two layers:
--
--   RAW      bank_connections / bank_accounts / bank_transactions / credit_card_bills /
--            investment_positions / investment_snapshots — a faithful, idempotent mirror of what
--            Pluggy returned (upserted by external_id), so classification rules can be re-run
--            without re-fetching and nothing is lost when a rule is wrong.
--   PROJECTED  the rows the Finanças screens already read (expenses, income_entries,
--            financial_balances), linked back to the raw row via bank_transaction_id /
--            bank_account_id — a link, never a second copy, so a sync can't double-count.
--
-- Money is numeric, never float. No credential or token is stored in these tables: the Pluggy
-- client id/secret/item ids are kept AES-GCM encrypted in external_integrations (type PLUGGY, see
-- internal/integrations), like every other provider connected under Configurações -> Conexões.
-- item_id here is only the opaque handle that ties raw rows to a connection.
-- ============================================================================================

CREATE TABLE bank_connections (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id             varchar(64) NOT NULL UNIQUE,
    connector_name      varchar(200),
    institution_name    varchar(200),
    status              varchar(40),
    execution_status    varchar(40),
    last_error          text,
    -- Last time Pluggy itself refreshed this item from the bank (Item.lastUpdatedAt).
    provider_updated_at timestamptz,
    -- Last time Atlas completed a full sync of this connection.
    last_synced_at      timestamptz,
    -- Set by POST /api/openfinance/sync; the worker picks it up on its next tick.
    sync_requested_at   timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE bank_accounts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id       uuid NOT NULL REFERENCES bank_connections(id) ON DELETE CASCADE,
    external_id         varchar(64) NOT NULL UNIQUE,
    account_type        varchar(20) NOT NULL CHECK (account_type IN ('BANK', 'CREDIT')),
    subtype             varchar(40),
    name                varchar(200),
    masked_number       varchar(60),
    balance             numeric(15,2),
    currency            varchar(8) NOT NULL DEFAULT 'BRL',
    credit_limit        numeric(15,2),
    available_credit    numeric(15,2),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_bank_accounts_connection_id ON bank_accounts (connection_id);

CREATE TABLE credit_card_bills (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES bank_accounts(id) ON DELETE CASCADE,
    external_id         varchar(64) NOT NULL UNIQUE,
    due_date            date NOT NULL,
    -- First day of the reference month (same "competência" convention documents use).
    competence          date NOT NULL,
    total_amount        numeric(15,2) NOT NULL,
    minimum_payment     numeric(15,2),
    allows_installments boolean,
    finance_charges     numeric(15,2) NOT NULL DEFAULT 0,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_credit_card_bills_account_due ON credit_card_bills (account_id, due_date);

CREATE TABLE bank_transactions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id          uuid NOT NULL REFERENCES bank_accounts(id) ON DELETE CASCADE,
    bill_id             uuid REFERENCES credit_card_bills(id) ON DELETE SET NULL,
    external_id         varchar(64) NOT NULL UNIQUE,
    transaction_date    date NOT NULL,
    -- Signed exactly as Pluggy sends it: for BANK accounts negative = money out; for CREDIT
    -- accounts positive = charge (raises the debt), negative = payment/refund.
    amount              numeric(15,2) NOT NULL,
    direction           varchar(10) NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    status              varchar(10) NOT NULL DEFAULT 'POSTED' CHECK (status IN ('PENDING', 'POSTED')),
    description         varchar(500),
    provider_category   varchar(200),
    installment_number  integer,
    installment_total   integer,
    -- What the classifier decided (see internal/openfinance/classify.go). Only EXPENSE/INCOME
    -- produce a row in expenses/income_entries; the IGNORED_* kinds are kept visible so the
    -- household can audit them and override a wrong call.
    projection_kind     varchar(30) NOT NULL DEFAULT 'PENDING'
                            CHECK (projection_kind IN ('PENDING', 'EXPENSE', 'INCOME',
                                'IGNORED_CARD_PAYMENT', 'IGNORED_TRANSFER', 'IGNORED_INVESTMENT',
                                'IGNORED_MANUAL')),
    -- Set when the household corrected the classifier; a re-sync never overwrites it.
    projection_override boolean NOT NULL DEFAULT false,
    removed_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_bank_transactions_account_date ON bank_transactions (account_id, transaction_date);
CREATE INDEX idx_bank_transactions_bill_id ON bank_transactions (bill_id);
CREATE INDEX idx_bank_transactions_projection ON bank_transactions (projection_kind);

CREATE TABLE investment_positions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id       uuid NOT NULL REFERENCES bank_connections(id) ON DELETE CASCADE,
    external_id         varchar(64) NOT NULL UNIQUE,
    name                varchar(300) NOT NULL,
    code                varchar(60),
    investment_type     varchar(30) NOT NULL DEFAULT 'OTHER',
    subtype             varchar(60),
    status              varchar(30),
    currency            varchar(8) NOT NULL DEFAULT 'BRL',
    balance             numeric(15,2) NOT NULL DEFAULT 0,
    amount_original     numeric(15,2),
    amount_profit       numeric(15,2),
    quantity            numeric(24,8),
    annual_rate         numeric(12,6),
    last_month_rate     numeric(12,6),
    last_12_months_rate numeric(12,6),
    due_date            date,
    last_synced_at      timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_investment_positions_connection_id ON investment_positions (connection_id);

-- One row per position per day: the sync upserts today's value, so the evolution chart has a
-- point per day the worker ran, without unbounded growth on repeated syncs the same day.
CREATE TABLE investment_snapshots (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    position_id         uuid NOT NULL REFERENCES investment_positions(id) ON DELETE CASCADE,
    recorded_on         date NOT NULL,
    balance             numeric(15,2) NOT NULL,
    UNIQUE (position_id, recorded_on)
);

-- ---- projection links on the tables the Finanças screens already read ----------------------

ALTER TABLE expenses
    ADD COLUMN source              varchar(10) NOT NULL DEFAULT 'MANUAL'
                                       CHECK (source IN ('MANUAL', 'DOCUMENT', 'PLUGGY')),
    ADD COLUMN bank_transaction_id uuid REFERENCES bank_transactions(id) ON DELETE SET NULL;
UPDATE expenses SET source = 'DOCUMENT' WHERE document_id IS NOT NULL;
CREATE UNIQUE INDEX uq_expenses_bank_transaction_id ON expenses (bank_transaction_id)
    WHERE bank_transaction_id IS NOT NULL;

ALTER TABLE income_entries
    ADD COLUMN source              varchar(10) NOT NULL DEFAULT 'MANUAL'
                                       CHECK (source IN ('MANUAL', 'DOCUMENT', 'PLUGGY')),
    ADD COLUMN bank_transaction_id uuid REFERENCES bank_transactions(id) ON DELETE SET NULL;
UPDATE income_entries SET source = 'DOCUMENT' WHERE document_id IS NOT NULL;
CREATE UNIQUE INDEX uq_income_entries_bank_transaction_id ON income_entries (bank_transaction_id)
    WHERE bank_transaction_id IS NOT NULL;

-- A balance linked to a bank account is owned by the sync (read-only in the UI); the existing
-- financial_balance_history keeps recording every change.
ALTER TABLE financial_balances
    ADD COLUMN bank_account_id uuid REFERENCES bank_accounts(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX uq_financial_balances_bank_account_id ON financial_balances (bank_account_id)
    WHERE bank_account_id IS NOT NULL;

-- Pluggy is one more provider in Configurações -> Conexões, stored like Telegram/Immich: credentials
-- encrypted in external_integrations. Additive widening of the type CHECK (see 000002).
ALTER TABLE external_integrations DROP CONSTRAINT IF EXISTS external_integrations_type_check;
ALTER TABLE external_integrations
    ADD CONSTRAINT external_integrations_type_check
    CHECK (type IN ('GOOGLE_DRIVE','GEMINI','DEEPSEEK','GROQ','OPENROUTER','TELEGRAM','IMMICH','OPENAI','OTHER','PLUGGY'));

-- Deleting a synced expense/income by hand means "don't count this": without this, the very next
-- sync would find the raw row unlinked and project it again. The trigger only fires for a LIVE
-- projection (raw row still EXPENSE/INCOME and not removed at the source) — the sync's own
-- retractions delete rows whose raw kind already changed, and must not mark them as manual.
CREATE FUNCTION bank_projection_deleted() RETURNS trigger AS $$
BEGIN
    IF OLD.bank_transaction_id IS NOT NULL AND OLD.source = 'PLUGGY' THEN
        UPDATE bank_transactions
           SET projection_kind = 'IGNORED_MANUAL', projection_override = true, updated_at = now()
         WHERE id = OLD.bank_transaction_id
           AND projection_kind IN ('EXPENSE', 'INCOME') AND removed_at IS NULL;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER trg_expenses_bank_projection_deleted BEFORE DELETE ON expenses
    FOR EACH ROW EXECUTE FUNCTION bank_projection_deleted();
CREATE TRIGGER trg_income_bank_projection_deleted BEFORE DELETE ON income_entries
    FOR EACH ROW EXECUTE FUNCTION bank_projection_deleted();

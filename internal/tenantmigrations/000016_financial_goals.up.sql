-- ============================================================================================
-- financial_goals — planning targets (buy a house, build a reserve, pay off a debt). What was
-- missing: a place to record "I want R$ 530k for a house, the down payment comes from FGTS + a
-- savings bucket, I can put aside R$ X/month". current_amount is a manual baseline; the real
-- accumulated figure adds the current value of any financial_balances listed in
-- funding_balance_ids (resolved at read time — no denormalised copy to keep in sync).
-- ============================================================================================
CREATE TABLE financial_goals (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 varchar(120) NOT NULL,
    kind                 varchar(20) NOT NULL,
    target_amount        numeric(15,2) NOT NULL,
    target_date          date,
    current_amount       numeric(15,2) NOT NULL DEFAULT 0,
    monthly_contribution numeric(15,2),
    funding_balance_ids  uuid[] NOT NULL DEFAULT '{}',
    notes                text,
    status               varchar(12) NOT NULL DEFAULT 'ACTIVE',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_financial_goals_kind CHECK (kind IN ('PURCHASE', 'RESERVE', 'DEBT_PAYOFF', 'OTHER')),
    CONSTRAINT ck_financial_goals_status CHECK (status IN ('ACTIVE', 'ACHIEVED', 'ARCHIVED')),
    CONSTRAINT ck_financial_goals_target_positive CHECK (target_amount > 0),
    CONSTRAINT ck_financial_goals_current_nonneg CHECK (current_amount >= 0),
    CONSTRAINT ck_financial_goals_contribution_nonneg CHECK (monthly_contribution IS NULL OR monthly_contribution >= 0)
);

CREATE INDEX idx_financial_goals_status ON financial_goals (status, target_date NULLS LAST, name);

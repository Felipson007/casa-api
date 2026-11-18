-- subscriptions — recurring charges (streaming, software, domains, gym, ...) tracked separately
-- from expenses: a subscription is a standing commitment (amount + cadence + next renewal), not a
-- single transaction. See docs/gap-analysis-vida-digital-20260822.md GAP 4.
CREATE TABLE subscriptions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id          uuid REFERENCES people(id) ON DELETE SET NULL,
    name               varchar(150) NOT NULL,
    category           varchar(20) NOT NULL DEFAULT 'OTHER'
                           CHECK (category IN ('STREAMING', 'SOFTWARE', 'DOMAIN', 'CLOUD', 'GYM', 'OTHER')),
    amount             numeric(12,2) NOT NULL,
    billing_cycle      varchar(20) NOT NULL CHECK (billing_cycle IN ('WEEKLY', 'MONTHLY', 'YEARLY')),
    -- calendar date, not timestamptz — same reasoning as documents.document_date/expiration_date:
    -- a renewal falls on a day, not a timezoned instant.
    next_renewal_date  date NOT NULL,
    payment_method     varchar(150),
    status             varchar(20) NOT NULL DEFAULT 'ACTIVE'
                           CHECK (status IN ('ACTIVE', 'CANCELLED', 'PAUSED')),
    notes              text,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_subscriptions_person_id ON subscriptions (person_id);
-- Backs the renewal-alert job (GAP 5.3) and the "próximos vencimentos" dashboard card.
CREATE INDEX idx_subscriptions_next_renewal_date ON subscriptions (next_renewal_date);

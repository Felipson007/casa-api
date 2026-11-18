-- Single, complete migration that creates the redesigned single-tenant schema from scratch.
-- This is the new app's starting point: it already reflects the production database state
-- (translated to English, single-tenant, with the architecture-plan.md improvements applied),
-- so there is no incremental migration history to carry forward.
--
-- Source of truth for the old->new mapping: docs/schema-migration-map.md (generated from a dump
-- of the production homelab database, kept at testdata/prod-dump.sql).
--
-- Applied rules (architecture-plan.md section 3 + 10):
--   * Single-tenant: NO tenant_id anywhere, no Row-Level Security.
--   * Every timestamp is timestamptz (never bare timestamp).
--   * Enum-like varchars get a CHECK with English UPPER_SNAKE_CASE values.
--   * Every FK child column gets an index.
--   * saldos_financeiros_historico stays TYPED (financial_balance_history), not audit_logs.
--   * pessoa_informacoes_historico folds into audit_logs (ETL concern, not a table here).
--   * member_permissions normalizes the 10 boolean columns of the old tenant_members.
--   * flyway_schema_history is deliberately NOT ported (golang-migrate has its own table).

-- ============================================================================================
-- app_settings — singleton row replacing the old tenants table (company branding, pix key).
-- ============================================================================================
CREATE TABLE app_settings (
    id           boolean PRIMARY KEY DEFAULT true CHECK (id),
    company_name varchar(255),
    pix_key      varchar(255),
    logo_path    varchar(500),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- people — pessoas. "gender" replaces the old parentesco heuristic (explicit attribute); the
-- relationship to self is derived from person_relationships by the Go personrelations package.
-- ============================================================================================
CREATE TABLE people (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       varchar(255) NOT NULL,
    gender     varchar(1) CHECK (gender IN ('M', 'F')),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- person_relationships — pessoa_relacoes. Only primary edges are stored; indirect derivation
-- (sibling, uncle, ...) is a Go function, not SQL.
-- ============================================================================================
CREATE TABLE person_relationships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_person_id  uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    to_person_id    uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    type            varchar(20) NOT NULL CHECK (type IN ('MOTHER_OF', 'FATHER_OF', 'SPOUSE_OF')),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_person_relationships_from_person_id ON person_relationships (from_person_id);
CREATE INDEX idx_person_relationships_to_person_id ON person_relationships (to_person_id);

-- v_person_relationships: materializes only the primary edges + names for the Go backend.
CREATE VIEW v_person_relationships AS
SELECT
    r.from_person_id,
    p_from.name AS from_name,
    r.type,
    r.to_person_id,
    p_to.name   AS to_name
FROM person_relationships r
JOIN people p_from ON p_from.id = r.from_person_id
JOIN people p_to   ON p_to.id   = r.to_person_id;

-- ============================================================================================
-- categories — categorias. parent_category_id is the self-FK, now indexed.
-- ============================================================================================
CREATE TABLE categories (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name               varchar(255) NOT NULL,
    parent_category_id uuid REFERENCES categories(id) ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED,
    type               varchar(10) CHECK (type IN ('EXPENSE', 'INCOME', 'ASSET')),
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_categories_parent_category_id ON categories (parent_category_id);

-- ============================================================================================
-- audit_logs — generic edit-trail for pure-audit *_historico tables (e.g. pessoa_informacoes_historico).
-- ============================================================================================
CREATE TABLE audit_logs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name varchar(100) NOT NULL,
    row_id     uuid NOT NULL,
    old_data   jsonb NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_logs_table_row ON audit_logs (table_name, row_id);

-- ============================================================================================
-- jobs — durable async job queue (internal/jobs). locked_until is the reclaim lease.
-- ============================================================================================
CREATE TABLE jobs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type         varchar(50) NOT NULL,
    payload      jsonb NOT NULL DEFAULT '{}',
    status       varchar(20) NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    retries      int NOT NULL DEFAULT 0,
    max_retries  int NOT NULL DEFAULT 3,
    result       jsonb,
    locked_until timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_jobs_status_locked_until ON jobs (status, locked_until);

-- ============================================================================================
-- app_users — auth users (single-tenant: OWNER or MEMBER, no tenant_id).
-- ============================================================================================
CREATE TABLE app_users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email        varchar(255) NOT NULL,
    role         varchar(20) NOT NULL CHECK (role IN ('OWNER', 'MEMBER')),
    password_hash varchar(255),
    name         varchar(255),
    last_login_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_app_users_email ON app_users (email);

-- ============================================================================================
-- member_permissions — normalized replacement for tenant_members' 10 boolean columns. One row per
-- (user, module); empty = no access. Seeded only when the owner grants access.
-- ============================================================================================
CREATE TABLE member_permissions (
    user_id   uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    module    varchar(30) NOT NULL CHECK (module IN ('categories', 'clients', 'loans', 'expenses',
        'income', 'assets', 'documents', 'people', 'personinfo', 'health', 'pets', 'reports',
        'notifications', 'integrations', 'finance')),
    can_read  boolean NOT NULL DEFAULT true,
    can_write boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, module)
);

-- ============================================================================================
-- password_reset_tokens
-- ============================================================================================
CREATE TABLE password_reset_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    token_hash varchar(255) NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_password_reset_tokens_token_hash ON password_reset_tokens (token_hash);

-- ============================================================================================
-- clients — clientes
-- ============================================================================================
CREATE TABLE clients (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       varchar(255) NOT NULL,
    phone      varchar(50),
    email      varchar(255),
    cpf_cnpj   varchar(20),
    notes      text,
    active     boolean NOT NULL DEFAULT true,
    pix_key    varchar(255),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- loans — emprestimos. monthly_interest_rate is numeric(12,6) (section 10, item 9).
-- ============================================================================================
CREATE TABLE loans (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id             uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    principal_amount      numeric(15,2) NOT NULL,
    monthly_interest_rate numeric(12,6) NOT NULL,
    payment_type          varchar(20) NOT NULL CHECK (payment_type IN ('INSTALLMENT', 'RECURRING_INTEREST')),
    installment_count     integer NOT NULL,
    contract_date         date NOT NULL,
    status                varchar(20) NOT NULL DEFAULT 'ACTIVE'
                              CHECK (status IN ('ACTIVE', 'OVERDUE', 'PAID_OFF', 'CANCELLED')),
    outstanding_balance   numeric(15,2) NOT NULL,
    due_day               integer NOT NULL,
    paid_off_date         date,
    legacy_source         varchar(255),
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_loans_client_id ON loans (client_id);
CREATE INDEX idx_loans_status ON loans (status);

-- ============================================================================================
-- installments — parcelas
-- ============================================================================================
CREATE TABLE installments (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_id             uuid NOT NULL REFERENCES loans(id) ON DELETE CASCADE,
    installment_number  integer NOT NULL,
    due_date            date NOT NULL,
    installment_amount  numeric(15,2) NOT NULL,
    outstanding_balance numeric(15,2) NOT NULL,
    principal_amount    numeric(15,2) NOT NULL,
    interest_amount     numeric(15,2) NOT NULL,
    status              varchar(20) NOT NULL DEFAULT 'PENDING'
                              CHECK (status IN ('PENDING', 'PAID', 'OVERDUE', 'CANCELLED')),
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_installments_loan_id ON installments (loan_id);
CREATE INDEX idx_installments_due_date ON installments (due_date);

-- ============================================================================================
-- loan_transactions — movimentacoes
-- ============================================================================================
CREATE TABLE loan_transactions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id     uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    loan_id       uuid REFERENCES loans(id) ON DELETE SET NULL,
    installment_id uuid REFERENCES installments(id) ON DELETE SET NULL,
    type          varchar(30) NOT NULL CHECK (type IN ('DISBURSEMENT', 'INSTALLMENT_PAYMENT',
        'ONE_OFF_PAYMENT', 'FORGIVENESS', 'AMORTIZATION')),
    amount        numeric(15,2) NOT NULL,
    payment_date  date NOT NULL,
    description   text,
    legacy_source varchar(255),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_loan_transactions_client_id ON loan_transactions (client_id);
CREATE INDEX idx_loan_transactions_loan_id ON loan_transactions (loan_id);

-- ============================================================================================
-- pets — created before documents/assets because those reference pets(id).
-- ============================================================================================
CREATE TABLE pets (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       varchar(200) NOT NULL,
    species    varchar(20) NOT NULL CHECK (species IN ('DOG', 'CAT', 'BIRD', 'FISH', 'OTHER')),
    breed      varchar(200),
    birth_date date,
    notes      varchar(2000),
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- assets — bens. attributes stays jsonb.
-- ============================================================================================
CREATE TABLE assets (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id         uuid REFERENCES people(id) ON DELETE SET NULL,
    name              varchar(200) NOT NULL,
    category_id       uuid REFERENCES categories(id) ON DELETE SET NULL,
    identification    varchar(300),
    acquisition_value numeric(15,2),
    acquisition_date  date,
    current_estimated_value numeric(15,2),
    status            varchar(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SOLD', 'INACTIVE')),
    has_debt          boolean NOT NULL DEFAULT false,
    debt_amount       numeric(15,2),
    notes             varchar(2000),
    attributes        jsonb NOT NULL DEFAULT '[]',
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_assets_person_id ON assets (person_id);
CREATE INDEX idx_assets_category_id ON assets (category_id);

-- ============================================================================================
-- documents — documentos (references people, assets, pets).
-- ============================================================================================
CREATE TABLE documents (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id         uuid REFERENCES people(id) ON DELETE SET NULL,
    original_filename varchar(255) NOT NULL,
    file_path         varchar(500) NOT NULL,
    file_type         varchar(20) NOT NULL CHECK (file_type IN ('PDF', 'PNG', 'JPEG', 'WEBP', 'OTHER')),
    origin            varchar(20) NOT NULL,
    document_date     date,
    tags              varchar(300),
    asset_id          uuid REFERENCES assets(id) ON DELETE SET NULL,
    pet_id            uuid REFERENCES pets(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_documents_person_id ON documents (person_id);
CREATE INDEX idx_documents_asset_id ON documents (asset_id);
CREATE INDEX idx_documents_pet_id ON documents (pet_id);

-- ============================================================================================
-- expenses — gastos
-- ============================================================================================
CREATE TABLE expenses (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id   uuid REFERENCES documents(id) ON DELETE SET NULL,
    category_id   uuid REFERENCES categories(id) ON DELETE SET NULL,
    person_id     uuid REFERENCES people(id) ON DELETE SET NULL,
    amount        numeric(15,2) NOT NULL,
    expense_date  date NOT NULL,
    description   varchar(300),
    reviewed      boolean NOT NULL DEFAULT false,
    original_date date,
    recurring     boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_expenses_category_id ON expenses (category_id);
CREATE INDEX idx_expenses_document_id ON expenses (document_id);
CREATE INDEX idx_expenses_person_id ON expenses (person_id);
CREATE INDEX idx_expenses_date ON expenses (expense_date);

-- ============================================================================================
-- income_entries — receitas
-- ============================================================================================
CREATE TABLE income_entries (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id   uuid REFERENCES documents(id) ON DELETE SET NULL,
    category_id   uuid REFERENCES categories(id) ON DELETE SET NULL,
    person_id     uuid REFERENCES people(id) ON DELETE SET NULL,
    amount        numeric(15,2) NOT NULL,
    income_date   date NOT NULL,
    original_date date,
    description   varchar(300),
    reviewed      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_income_category_id ON income_entries (category_id);
CREATE INDEX idx_income_document_id ON income_entries (document_id);
CREATE INDEX idx_income_person_id ON income_entries (person_id);
CREATE INDEX idx_income_date ON income_entries (income_date);

-- ============================================================================================
-- pet_records — registros_pet
-- ============================================================================================
CREATE TABLE pet_records (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id      uuid NOT NULL REFERENCES pets(id) ON DELETE CASCADE,
    type        varchar(30) NOT NULL,
    name        varchar(200),
    record_date date NOT NULL,
    status      varchar(20) NOT NULL DEFAULT 'DONE' CHECK (status IN ('DONE', 'PENDING', 'CANCELLED')),
    amount      numeric(10,2),
    notes       varchar(2000),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_pet_records_pet_id ON pet_records (pet_id);

-- ============================================================================================
-- health_marker_catalog / health_records — marcadores_exame_catalogo / registros_saude
-- ============================================================================================
CREATE TABLE health_marker_catalog (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  varchar(100) NOT NULL,
    unit                  varchar(20),
    reference_min         numeric(10,2),
    reference_max         numeric(10,2),
    related_organ         varchar(30),
    created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE health_records (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id         uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    type              varchar(30) NOT NULL,
    record_date       date NOT NULL,
    status            varchar(20) NOT NULL DEFAULT 'DONE' CHECK (status IN ('DONE', 'PENDING', 'CANCELLED')),
    amount            numeric(10,2),
    unit              varchar(20),
    marker_catalog_id uuid REFERENCES health_marker_catalog(id) ON DELETE SET NULL,
    organ             varchar(30),
    assessment_status varchar(20),
    document_id       uuid REFERENCES documents(id) ON DELETE SET NULL,
    notes             text,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_health_records_person_id ON health_records (person_id);
CREATE INDEX idx_health_records_marker_id ON health_records (marker_catalog_id);

-- ============================================================================================
-- financial_balances / financial_balance_history — saldos_financeiros (TYPED, not audit_logs).
-- ============================================================================================
CREATE TABLE financial_balances (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id   uuid REFERENCES people(id) ON DELETE SET NULL,
    name        varchar(200) NOT NULL,
    balance_type varchar(30) NOT NULL CHECK (balance_type IN ('FGTS', 'PRIVATE_PENSION', 'SAVINGS',
        'CHECKING', 'OTHER')),
    current_value numeric(15,2) NOT NULL,
    updated_on  date NOT NULL,
    notes       varchar(2000),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_financial_balances_person_id ON financial_balances (person_id);

CREATE TABLE financial_balance_history (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    balance_id  uuid NOT NULL REFERENCES financial_balances(id) ON DELETE CASCADE,
    balance     numeric(15,2) NOT NULL,
    recorded_on date NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_fb_history_balance_id ON financial_balance_history (balance_id);

-- ============================================================================================
-- notifications / notification_configuration
-- ============================================================================================
CREATE TABLE notifications (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type      varchar(50) NOT NULL,
    title     varchar(200) NOT NULL,
    message   varchar(500) NOT NULL,
    link      varchar(300),
    read      boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notification_configuration (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    retention_days smallint NOT NULL DEFAULT 7 CHECK (retention_days >= 1 AND retention_days <= 365),
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- chat_messages — chat_mensagens. role CHECK; tool_calls jsonb.
-- ============================================================================================
CREATE TABLE chat_messages (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_user_id  uuid NOT NULL REFERENCES app_users(id) ON DELETE CASCADE,
    role         varchar(20) NOT NULL CHECK (role IN ('USER', 'ASSISTANT', 'SYSTEM')),
    content      text NOT NULL,
    model_used   varchar(100),
    tool_calls   jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_chat_messages_app_user_id ON chat_messages (app_user_id);

-- ============================================================================================
-- ai_configurations — ia_configuracoes. provider_order jsonb (was CSV).
-- ============================================================================================
CREATE TABLE ai_configurations (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_order jsonb NOT NULL DEFAULT '[]',
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- external_integrations — integracoes_externas. models jsonb (was CSV); immich_base_url kept.
-- ============================================================================================
CREATE TABLE external_integrations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type                  varchar(30) NOT NULL CHECK (type IN ('IMMICH', 'TELEGRAM', 'OPENAI', 'OTHER')),
    connected_account     varchar(255),
    refresh_token_encrypted bytea NOT NULL,
    refresh_token_iv      bytea NOT NULL,
    models                jsonb,
    last_telegram_update_id bigint,
    telegram_pairing_code varchar(10),
    telegram_pairing_expires_at timestamptz,
    immich_base_url       varchar(255),
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- system_integrations — integracoes_sistema (no tenant_id, no person scoping).
-- ============================================================================================
CREATE TABLE system_integrations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type                  varchar(30) NOT NULL CHECK (type IN ('IMMICH', 'TELEGRAM', 'OPENAI', 'OTHER')),
    connected_account     varchar(255),
    refresh_token_encrypted bytea NOT NULL,
    refresh_token_iv      bytea NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- backup_configuration — backup_configuracao
-- ============================================================================================
CREATE TABLE backup_configuration (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hour                     smallint NOT NULL DEFAULT 3 CHECK (hour >= 0 AND hour <= 23),
    minute                   smallint NOT NULL DEFAULT 0 CHECK (minute >= 0 AND minute <= 59),
    last_automatic_backup_at timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now()
);

-- ============================================================================================
-- person_information — pessoa_informacoes (the _historico variant folds into audit_logs).
-- ============================================================================================
CREATE TABLE person_information (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id        uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    category         varchar(20) NOT NULL CHECK (category IN ('PERSONAL', 'ACADEMIC', 'PROFESSIONAL', 'MEDICAL', 'OTHER')),
    key              varchar(200) NOT NULL,
    value            varchar(1000) NOT NULL,
    document_id      uuid REFERENCES documents(id) ON DELETE SET NULL,
    origin           varchar(20) NOT NULL DEFAULT 'MANUAL' CHECK (origin IN ('MANUAL', 'IMPORT', 'OCR')),
    document_type    varchar(100),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_person_information_person_id ON person_information (person_id);

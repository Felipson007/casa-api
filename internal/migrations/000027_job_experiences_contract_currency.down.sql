ALTER TABLE job_experiences
    DROP CONSTRAINT IF EXISTS ck_job_experiences_contract_type,
    DROP COLUMN IF EXISTS contract_type,
    DROP COLUMN IF EXISTS usd_amount,
    DROP COLUMN IF EXISTS exchange_rate;

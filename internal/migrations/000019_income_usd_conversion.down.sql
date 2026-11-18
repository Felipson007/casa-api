ALTER TABLE income_entries
    DROP COLUMN IF EXISTS usd_amount,
    DROP COLUMN IF EXISTS exchange_rate;

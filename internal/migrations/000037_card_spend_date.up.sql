-- Card spending counts on the PURCHASE date, not at the statement's competência
-- (internal/platform/cardspend): installment n of a purchase counts n-1 months after it. Bill
-- payments are cash and are reported from credit_card_bills by due_date.
--
-- bank_transactions.transaction_date becomes the purchase day for card charges (Pluggy's
-- creditCardMetadata.purchaseDate); the connector's own date is kept in posted_date, which the
-- sync window and the removal sweep are expressed in. card_last4 records which card (titular,
-- adicional, virtual) was used — informational only; reports do not split by it.
ALTER TABLE bank_transactions
    ADD COLUMN posted_date date,
    ADD COLUMN card_last4 varchar(4);
UPDATE bank_transactions SET posted_date = transaction_date WHERE posted_date IS NULL;

-- Re-date existing card expenses with the same rule. Statement (DOCUMENT) lines read "n/N" from
-- the description; synced (PLUGGY) card rows take it from their raw transaction. The adjustment
-- line of a reconciled statement has original_date = expense_date (the competência) and stays put.
-- Not reversible by the down migration: the previous expense_date was the competência, derivable
-- again from credit_card_bills / documents.statement_closing_date if ever needed.
UPDATE expenses x
   SET expense_date = (x.original_date + make_interval(months => GREATEST(
           COALESCE((regexp_match(x.description, '(\d{1,2})/(\d{1,2})\)?\s*$'))[1]::int, 1) - 1, 0)))::date
 WHERE x.source = 'DOCUMENT' AND x.original_date IS NOT NULL
   AND (regexp_match(x.description, '(\d{1,2})/(\d{1,2})\)?\s*$') IS NULL
        OR ((regexp_match(x.description, '(\d{1,2})/(\d{1,2})\)?\s*$'))[2]::int >= 2
            AND (regexp_match(x.description, '(\d{1,2})/(\d{1,2})\)?\s*$'))[1]::int
                BETWEEN 1 AND (regexp_match(x.description, '(\d{1,2})/(\d{1,2})\)?\s*$'))[2]::int));

UPDATE expenses x
   SET expense_date = (t.transaction_date + make_interval(months => GREATEST(COALESCE(t.installment_number, 1) - 1, 0)))::date,
       original_date = t.transaction_date
  FROM bank_transactions t
  JOIN bank_accounts a ON a.id = t.account_id
 WHERE x.bank_transaction_id = t.id AND a.account_type = 'CREDIT' AND x.source = 'PLUGGY'
   AND x.document_id IS NULL;

-- One full re-sync (the first-sync 365-day window) so card charges the old 14-day incremental
-- window never fetched, and rows dated on the statement day, come back with their purchase dates.
UPDATE bank_connections SET last_synced_at = NULL;

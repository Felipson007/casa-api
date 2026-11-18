-- ============================================================================================
-- ai_insights.type gained a 'LOANS' value in the curator code + prompt (internal/curator:
-- "Classifique análises da carteira de empréstimos como LOANS") and in sanitiseInsights, but the
-- CHECK from 000012 was never widened. Every daily curator run that produced a loan-portfolio
-- insight failed the whole transaction ("new row for relation \"ai_insights\" violates check
-- constraint \"ck_ai_insights_type\""), so no insights were saved at all.
-- ============================================================================================
ALTER TABLE ai_insights DROP CONSTRAINT ck_ai_insights_type;
ALTER TABLE ai_insights
    ADD CONSTRAINT ck_ai_insights_type CHECK (type IN ('FINANCIAL', 'LOANS', 'HEALTH', 'ORGANIZATION'));

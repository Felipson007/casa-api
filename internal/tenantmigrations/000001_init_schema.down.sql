-- Rollback for 000001_init_schema.up.sql (the single complete schema). Drop order reverses
-- creation; objects that are referenced by others must be dropped first (e.g. documents before
-- pets, because documents.pet_id references pets.id).
DROP TABLE IF EXISTS person_information;
DROP TABLE IF EXISTS backup_configuration;
DROP TABLE IF EXISTS system_integrations;
DROP TABLE IF EXISTS external_integrations;
DROP TABLE IF EXISTS ai_configurations;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS notification_configuration;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS financial_balance_history;
DROP TABLE IF EXISTS financial_balances;
DROP TABLE IF EXISTS health_records;
DROP TABLE IF EXISTS health_marker_catalog;
DROP TABLE IF EXISTS pet_records;
DROP TABLE IF EXISTS income_entries;
DROP TABLE IF EXISTS expenses;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS pets;
DROP TABLE IF EXISTS loan_transactions;
DROP TABLE IF EXISTS installments;
DROP TABLE IF EXISTS loans;
DROP TABLE IF EXISTS clients;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS categories;
DROP VIEW IF EXISTS v_person_relationships;
DROP TABLE IF EXISTS person_relationships;
DROP TABLE IF EXISTS people;
DROP TABLE IF EXISTS app_settings;

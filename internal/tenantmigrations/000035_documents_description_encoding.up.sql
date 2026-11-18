-- Same as internal/migrations/000038_documents_description_encoding.up.sql, mirrored into the
-- per-tenant schema baseline (see internal/tenantmigrations/embed.go for why the two trees exist).
-- The generated search summaries ("Documento do tipo ...") were written with mojibake
-- ("patrimÃ´nio", "saÃºde", "nÃ£o"): internal/documents/metadata.go held the labels
-- double-encoded, and 000011 backfilled the same text. Repair the stored descriptions; the
-- source labels are fixed in the same change.
UPDATE documents
   SET description = replace(replace(replace(replace(replace(replace(replace(replace(replace(replace(replace(replace(description, 'Ã´', 'ô'), 'Ãº', 'ú'), 'Ã£', 'ã'), 'Ã§', 'ç'), 'Ã©', 'é'), 'Ã¡', 'á'), 'Ãª', 'ê'), 'Ã³', 'ó'), 'Ãµ', 'õ'), 'Ã¢', 'â'), 'Ã­', 'í'), 'Ã‰', 'É')
 WHERE description LIKE '%Ã%';

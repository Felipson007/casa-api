-- Give every existing document a compact semantic summary and machine-readable tags. These fields
-- are intentionally provider-neutral so future search/RAG/assistant integrations can index them.
UPDATE documents
SET description = COALESCE(
        NULLIF(BTRIM(description), ''),
        'Documento do tipo ' || CASE origin
            WHEN 'INVOICE' THEN 'fatura financeira'
            WHEN 'PAYSTUB' THEN 'holerite ou comprovante de renda'
            WHEN 'LAB_RESULT' THEN 'exame ou laudo de saÃºde'
            WHEN 'PERSONAL' THEN 'documento pessoal'
            WHEN 'ASSET' THEN 'documento de patrimÃ´nio'
            WHEN 'PET' THEN 'documento de pet'
            WHEN 'INSURANCE' THEN 'documento de seguro'
            WHEN 'WARRANTY' THEN 'documento de garantia'
            WHEN 'TAX' THEN 'documento fiscal'
            ELSE 'documento ainda nÃ£o classificado'
        END || ': ' || REGEXP_REPLACE(original_filename, '[_-]+', ' ', 'g') || '.'
    ),
    tags = COALESCE(NULLIF(BTRIM(tags), ''), 'origem:' || LOWER(origin) || ',arquivo:' || LOWER(file_type));

-- ============================================================================================
-- job_experiences — structured career history (company, title, level, salary, period), replacing
-- the old free-text approach of deriving "Carreira" from generic person_information facts + paystub
-- documents. end_date NULL means "current job" (IsCurrent is derived at the DTO layer, never
-- persisted). resume_document_id optionally links a filed résumé (documents.origin = 'RESUME') —
-- never auto-extracted, purely a reference kept alongside the manually-entered structured data.
-- ============================================================================================
CREATE TABLE job_experiences (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id           uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    company             varchar(200) NOT NULL,
    title               varchar(200) NOT NULL,
    position_level      varchar(200),
    salary              numeric(15,2),
    start_date          date NOT NULL,
    end_date            date,
    notes               text,
    resume_document_id  uuid REFERENCES documents(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_job_experiences_dates CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE INDEX idx_job_experiences_person_id ON job_experiences (person_id);

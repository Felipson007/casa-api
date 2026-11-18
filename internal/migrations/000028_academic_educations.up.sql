-- ============================================================================================
-- academic_educations — structured education history (institution, course, level, period),
-- mirroring job_experiences (000020) but for the "Educação" screen: replaces the old free-text
-- approach of deriving education from generic person_information facts + diploma documents.
-- end_date NULL means "cursando" (IsCurrent is derived at the DTO layer, never persisted).
-- diploma_document_id optionally links a filed diploma/certificate (documents.origin — no
-- dedicated value exists yet, so this stays a free reference, same convention as
-- job_experiences.resume_document_id).
-- ============================================================================================
CREATE TABLE academic_educations (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id             uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    institution           varchar(200) NOT NULL,
    course                varchar(200) NOT NULL,
    education_level       varchar(200),
    start_date            date NOT NULL,
    end_date              date,
    notes                 text,
    diploma_document_id   uuid REFERENCES documents(id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_academic_educations_dates CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE INDEX idx_academic_educations_person_id ON academic_educations (person_id);

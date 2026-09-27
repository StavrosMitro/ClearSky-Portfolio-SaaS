-- +goose Up
-- A grading = one course in one exam period (SRS 1.1.2: NULL → open → final).
CREATE TABLE gradings (
  id               uuid PRIMARY KEY,
  institution_id   uuid NOT NULL,
  course_code      text NOT NULL,
  course_title     text NOT NULL,
  period           text NOT NULL,
  state            text NOT NULL CHECK (state IN ('open', 'final')),
  version          int  NOT NULL CHECK (version > 0),
  instructor_id    uuid,
  origin           text NOT NULL DEFAULT 'upload' CHECK (origin IN ('upload', 'migration')),
  grading_scale    text,
  question_weights numeric[] NOT NULL DEFAULT '{}',
  opened_at        timestamptz NOT NULL,
  finalized_at     timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (institution_id, course_code, period),
  CHECK ((state = 'final') = (finalized_at IS NOT NULL)),
  CHECK (instructor_id IS NOT NULL OR origin = 'migration')
);
CREATE INDEX gradings_version ON gradings (version);

-- Every uploaded workbook with its preview (SRS 2.5: CONFIRM / CANCEL).
CREATE TABLE uploads (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id uuid NOT NULL,
  kind           text NOT NULL CHECK (kind IN ('initial', 'final')),
  status         text NOT NULL CHECK (status IN ('parsed', 'rejected', 'confirmed', 'cancelled')),
  uploaded_by    uuid NOT NULL,
  filename       text NOT NULL,
  file           bytea NOT NULL,
  file_sha256    text NOT NULL,
  grading_id     uuid,
  course_code    text,
  course_title   text,
  period         text,
  parsed         jsonb,
  problems       jsonb NOT NULL DEFAULT '[]',
  created_at     timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  confirmed_at   timestamptz
);
CREATE INDEX uploads_uploader ON uploads (uploaded_by, created_at DESC);

CREATE TABLE grades (
  grading_id      uuid NOT NULL REFERENCES gradings (id) ON DELETE CASCADE,
  student_id      text NOT NULL,
  student_name    text,
  total           numeric(4,2),
  question_scores numeric[] NOT NULL DEFAULT '{}',
  upload_id       uuid REFERENCES uploads (id),
  PRIMARY KEY (grading_id, student_id)
);

-- +goose Down
DROP TABLE grades;
DROP TABLE uploads;
DROP TABLE gradings;

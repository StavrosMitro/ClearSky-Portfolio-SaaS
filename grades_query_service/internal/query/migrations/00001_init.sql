-- +goose Up
-- Read models built from grades-ingest snapshots. Disposable: reconcile
-- rebuilds everything from grades-ingest.
CREATE TABLE course_gradings (
  grading_id           uuid PRIMARY KEY,
  institution_id       uuid NOT NULL,
  course_code          text NOT NULL,
  course_title         text NOT NULL,
  period               text NOT NULL,
  instructor_id        uuid,
  state                text NOT NULL CHECK (state IN ('open', 'final')),
  version              int  NOT NULL,
  question_weights     double precision[] NOT NULL DEFAULT '{}',
  student_count        int  NOT NULL,
  initial_published_at timestamptz NOT NULL,
  final_published_at   timestamptz,
  updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX course_gradings_scope ON course_gradings (institution_id, period);
CREATE INDEX course_gradings_instructor ON course_gradings (instructor_id);

CREATE TABLE personal_grades (
  grading_id      uuid NOT NULL REFERENCES course_gradings (grading_id) ON DELETE CASCADE,
  institution_id  uuid NOT NULL,
  student_id      text NOT NULL,
  total           double precision,
  question_scores double precision[], -- NULL once final (SRS REQ016)
  PRIMARY KEY (grading_id, student_id)
);
CREATE INDEX personal_grades_student ON personal_grades (institution_id, student_id);

-- Computed once per version: one read instead of eleven GROUP BY queries.
CREATE TABLE grade_distributions (
  grading_id  uuid PRIMARY KEY REFERENCES course_gradings (grading_id) ON DELETE CASCADE,
  version     int   NOT NULL,
  histograms  jsonb NOT NULL,
  computed_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE grade_distributions;
DROP TABLE personal_grades;
DROP TABLE course_gradings;

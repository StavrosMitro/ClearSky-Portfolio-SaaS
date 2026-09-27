-- +goose Up
-- What reviews needs from each grading; kept in sync by the orchestrator.
CREATE TABLE gradings (
  grading_id     uuid PRIMARY KEY,
  institution_id uuid NOT NULL,
  course_code    text NOT NULL,
  course_title   text NOT NULL,
  period         text NOT NULL,
  instructor_id  uuid,
  state          text NOT NULL CHECK (state IN ('open', 'final')),
  version        int  NOT NULL
);
CREATE INDEX gradings_instructor ON gradings (instructor_id);

CREATE TABLE review_requests (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  grading_id      uuid NOT NULL REFERENCES gradings (grading_id),
  institution_id  uuid NOT NULL,
  student_id      text NOT NULL,
  student_user_id uuid NOT NULL,
  student_display text NOT NULL,
  message         text NOT NULL,
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'answered', 'closed')),
  reply_action    text CHECK (reply_action IN ('total_accept', 'partial_accept', 'reject')),
  reply_message   text,
  replied_by      uuid,
  attachment_key  text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  replied_at      timestamptz,
  UNIQUE (grading_id, student_id), -- one request per student and grading (SRS 2.7)
  CHECK ((status = 'answered') = (reply_action IS NOT NULL))
);
CREATE INDEX review_requests_inbox ON review_requests (grading_id, status);

-- +goose Down
DROP TABLE review_requests;
DROP TABLE gradings;

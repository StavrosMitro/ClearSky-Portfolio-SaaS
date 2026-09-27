-- +goose Up
CREATE TABLE email_outbox (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  uuid,
  recipient       text NOT NULL,
  template        text NOT NULL,
  payload         jsonb,           -- subject and body; deleted once sent (contains links)
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  dedupe_key      text UNIQUE,
  created_at      timestamptz NOT NULL DEFAULT now(),
  sent_at         timestamptz
);
CREATE INDEX email_outbox_due ON email_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX email_outbox_sent ON email_outbox (sent_at) WHERE status = 'sent';

-- +goose Down
DROP TABLE email_outbox;

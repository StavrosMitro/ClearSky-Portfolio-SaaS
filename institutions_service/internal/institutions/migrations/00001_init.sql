-- +goose Up
CREATE TABLE institutions (
  id            uuid PRIMARY KEY,
  name          text NOT NULL,
  contact_email text NOT NULL,
  director      text,
  email_domains text[] NOT NULL DEFAULT '{}',
  status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX institutions_name ON institutions (lower(name));

-- Current balance; changes in the same transaction as the ledger.
CREATE TABLE credit_balances (
  institution_id uuid PRIMARY KEY REFERENCES institutions (id),
  balance        int NOT NULL CHECK (balance >= 0),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Every movement, forever (append-only).
CREATE TABLE credit_ledger (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  uuid NOT NULL REFERENCES institutions (id),
  delta           int NOT NULL CHECK (delta <> 0),
  reason          text NOT NULL CHECK (reason IN ('purchase', 'grading_charge', 'refund', 'adjustment')),
  reference       text,
  idempotency_key text NOT NULL UNIQUE,
  created_by      uuid,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX credit_ledger_institution ON credit_ledger (institution_id, created_at DESC);

-- +goose Down
DROP TABLE credit_ledger;
DROP TABLE credit_balances;
DROP TABLE institutions;

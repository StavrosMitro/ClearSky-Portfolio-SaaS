-- +goose Up
-- Empty strings mean "not set" for password_hash, student_id and full_name
-- (simpler with the ORM than NULL; see docs/data-model.md).
CREATE TABLE users (
  id                  uuid PRIMARY KEY,
  institution_id      uuid NOT NULL,
  username            text NOT NULL UNIQUE,
  role                text NOT NULL CHECK (role IN ('student', 'instructor', 'institution_representative')),
  student_id          text NOT NULL DEFAULT '',
  full_name           text NOT NULL DEFAULT '',
  password_hash       text NOT NULL DEFAULT '',
  google_sub          text UNIQUE,
  password_changed_at timestamptz,
  failed_logins       int NOT NULL DEFAULT 0,
  locked_until        timestamptz,
  disabled_at         timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CHECK ((role = 'student') = (student_id <> ''))
);
CREATE UNIQUE INDEX users_username_lower ON users (lower(username));
CREATE UNIQUE INDEX users_student_id ON users (institution_id, student_id) WHERE student_id <> '';

CREATE TABLE student_roster (
  institution_id uuid NOT NULL,
  student_id     text NOT NULL,
  email          text NOT NULL,
  full_name      text NOT NULL DEFAULT '',
  uploaded_by    uuid NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (institution_id, student_id),
  UNIQUE (institution_id, email)
);
CREATE INDEX student_roster_email ON student_roster (email);

CREATE TABLE account_tokens (
  id             uuid PRIMARY KEY,
  token_hash     text NOT NULL UNIQUE, -- SHA-256; the token itself is never stored
  purpose        text NOT NULL CHECK (purpose IN ('student_activation', 'set_password', 'password_reset', 'google_signup')),
  institution_id uuid NOT NULL,
  email          text NOT NULL,
  student_id     text NOT NULL DEFAULT '',
  user_id        uuid REFERENCES users (id) ON DELETE CASCADE,
  expires_at     timestamptz NOT NULL,
  used_at        timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_tokens_email ON account_tokens (purpose, email);

-- +goose Down
DROP TABLE account_tokens;
DROP TABLE student_roster;
DROP TABLE users;

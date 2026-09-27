# Identity service

Accounts of every role, the student registry, and the application JWTs.
Identity is the only service that creates accounts, decides roles or signs
tokens (docs/service-contracts.md). Formerly `user_management_service`.

## Interfaces

**RabbitMQ** — queue `clearsky.identity.commands.v1`, routing key
`auth.request`, v1 envelope. The `type` field selects the operation:

| Type | Caller | Does |
|---|---|---|
| `login` | anyone | Password sign-in; returns the JWT, role and user ID. Per-account backoff after failures. |
| `google_token_login` | anyone | Sign-in with a Google ID token, verified here. |
| `change_password` | signed-in user | |
| `request_password_reset` | anyone | Emails a reset link (same answer whether or not the account exists). |
| `request_student_activation` | anyone | Student ID + email must match the registry; emails an activation link. |
| `complete_activation` | link holder | Sets the password from an activation, invitation or reset link. |
| `complete_google_signup` | Google signup ticket | First Google sign-in of a registry student. |
| `create_instructor` | representative (`actor_token`) | Creates the instructor and emails an invitation. |
| `import_student_roster` | representative (`actor_token`) | Imports the registry CSV (all or nothing). |

**HTTP** (port 8080, behind the proxy): `GET /auth/google/login` and
`/auth/google/callback` (the browser OAuth flow; the callback redirects to
the front-end), and `/health/live`, `/health/ready`.

Emails are not sent here: identity publishes them to notifications
(`notifications.email`), which delivers them from its outbox.

## Data

PostgreSQL (`identity_db`), migrations in `internal/store/migrations`:
`users` (with `institution_id`, bcrypt `password_hash`, `google_sub`; a
student always has a `student_id`), `student_roster` (per institution), and
`account_tokens` (hashed, single-use link tokens).

## Configuration

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL`, `AMQP_URL` | yes | Set by Compose |
| `JWT_SECRET` | yes | HS256 key, 32+ characters, shared with the orchestrator |
| `JWT_ISSUER` / `JWT_AUDIENCE` | no | Default `clearsky-identity` / `clearsky-api` |
| `PUBLIC_APP_URL` | yes | Base URL of emailed links (`https://localhost` locally) |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL`, `GOOGLE_ALLOWED_DOMAINS`, `GOOGLE_REQUIRE_WORKSPACE` | for Google | See docs/account-onboarding.md |
| `BOOTSTRAP_ADMIN_USERNAME` / `BOOTSTRAP_ADMIN_PASSWORD` / `BOOTSTRAP_INSTITUTION` | no | First secretariat account; see docs/auth-cutover.md |

## Layout

```
identity_service/
├── cmd/identity/        # entry point (contracts/app)
├── internal/
│   ├── accounts/        # onboarding, sign-in, registry, tokens
│   ├── config/          # bootstrap administrator
│   ├── google/          # OAuth flow, ID-token verification, domain policy
│   ├── messaging/       # the auth.request handler
│   ├── model/           # GORM models
│   ├── notifier/        # publishes emails to notifications
│   └── store/           # PostgreSQL connection and migrations
└── pkg/jwt/             # token issuing
```

## Development

```bash
docker compose up -d --build identity          # from the repository root
TEST_DATABASE_URL=… go test ./...              # tests use a real PostgreSQL
```

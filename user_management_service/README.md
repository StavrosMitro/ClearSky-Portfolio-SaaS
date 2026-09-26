# User Management Microservice

This microservice handles user authentication and authorization for the clearSKY application, part of the SaaS Technologies course (NTUA, Spring 2024–2025).

## Technologies Used

- GoLang 1.22
- RabbitMQ for asynchronous messaging
- SQLite with GORM ORM
- Docker and Docker Compose
- JWT for stateless authentication

## Supported Features

### Internal HTTP Endpoints

The HTTP port is internal to the Compose network; the orchestrator is the public API.

| Endpoint                 | Method | Description |
|--------------------------|--------|-------------|
| `/internal/google-login` | POST   | Exchange an email already verified by Google auth for an application JWT. Requires `Authorization: Bearer $INTERNAL_AUTH_TOKEN`. |
| `/auth/validate`         | GET    | Validate a JWT via middleware |
| `/auth/profile`          | GET    | Return the authenticated user's profile |

### RabbitMQ Messaging

- **Exchange:** `clearsky.commands.v1` (direct)
- **Queue:** `clearsky.auth.commands.v1`, bound with routing key `auth.request`
- **Request types:** `login`, `change_password`, `google_login`, `request_student_activation`,
  `complete_activation`, `complete_google_signup`, and (representatives only, with `actor_token`)
  `create_instructor`, `import_student_roster`. See `docs/account-onboarding.md`.
- **Reply Queue:** Defined by the `reply_to` field in the request
- **Correlation ID:** Copied from the request

#### Sample Request (RabbitMQ)

```json
{
  "type": "login",
  "username": "student@example.com",
  "password": "mypassword123"
}
```

#### Sample Response

Replies use the v1 envelope described in `docs/service-contracts.md`:

```json
{"version":1,"data":{"token":"<jwt_token_here>","role":"student","user_id":"<uuid>"}}
```

```json
{"version":1,"error":{"code":"INVALID_CREDENTIALS","message":"Invalid credentials","retryable":false}}
```

## Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `JWT_SECRET` | yes | HS256 signing key, 32+ characters, shared with the orchestrator |
| `INTERNAL_AUTH_TOKEN` | yes | Shared secret for `/internal/google-login`, 32+ characters |
| `JWT_ISSUER` / `JWT_AUDIENCE` | no | Default `clearsky-identity` / `clearsky-api` |
| `BOOTSTRAP_ADMIN_USERNAME` / `BOOTSTRAP_ADMIN_PASSWORD` | no | Set both to create a first institution representative (password 12+ characters). No account is created otherwise. |
| `DATABASE_DSN` | no | SQLite file, default `auth_service.db` |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | for email | SMTP relay for confirmation links and invitations; unset disables them |
| `PUBLIC_APP_URL` | no | Front-end base URL used in emailed links, default `http://localhost:3000` |

See `docs/auth-cutover.md` for deployment and remediation steps.

## Execution Instructions (Dockerized)

1. Ensure `docker` and `docker-compose` are installed.
2. Build and start the services:

```bash
docker-compose up --build
```

3. Access Points:
   - RabbitMQ Management UI: http://localhost:15672
     - Username: `guest`
     - Password: `guest`

## Project Structure

```
user_management_service/
├── cmd/                    # Application entry point
├── internal/
│   ├── config/             # Database configuration
│   ├── handler/            # HTTP handlers
│   ├── messaging/          # RabbitMQ consumer and producer logic
│   ├── middleware/         # JWT validation logic
│   └── model/              # GORM models
├── pkg/jwt/                # JWT utility functions
├── Dockerfile
├── docker-compose.yml
└── README.md
```

## Environment

- The project uses a multi-stage Docker build for optimized container size.
- JWT tokens expire after 24 hours and are signed using HS256.
- SQLite database is stored locally in `auth_service.db`.

## Authors

- clearSKY Project Team [Group 12]
- National Technical University of Athens (NTUA)
- Course: Software as a Service Technologies (2024–2025)

## Implementation Status

| Feature                      | Status |
|-----------------------------|--------|
| Student registration        | Done   |
| JWT-based login             | Done   |
| Token validation            | Done   |
| RabbitMQ message handling   | Done   |
| Docker support              | Done   |
| Role included in JWT        | Done   |

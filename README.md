# Clear Sky SaaS Platform

**NTUA ECE SAAS 2025 – Team 12**

---

## Project Overview

Clear Sky is a modular, production-grade SaaS platform for academic institutions, designed to manage student grades, review workflows, user authentication, institutional credits, and more. The system is architected as a set of loosely coupled microservices, communicating asynchronously via RabbitMQ, and is fully containerized for easy deployment and scalability.

This project was developed following a formal Software Requirements Specification (SRS) and is accompanied by comprehensive UML documentation (Visual Paradigm Project, VPP), ensuring maintainability, clarity, and extensibility.

---

## Key Features

- **Accounts:** password and Google sign-in, HttpOnly JWT sessions, role-based access (secretariat, instructor, student). Students register against the secretariat's registry; instructors are invited by the secretariat.
- **Grade publishing:** instructors upload the e-sec workbook, see a preview and confirm or cancel it. Initial grades open a grading (one credit); final grades close it.
- **Personal grades and statistics:** students see their own grades per question; everyone at the institution sees the grade distributions.
- **Review workflow:** one review request per student and grading; instructors accept, partially accept or reject; requests still pending close when the final grades are published.
- **Institution credits:** purchases and charges in an auditable ledger; a grading is never charged twice.

---

## Architecture

```
browser ── proxy (Caddy, HTTPS) ─┬─ frontend (Express/EJS)
                                 ├─ /api/*         → orchestrator (API gateway, Go/Gin)
                                 └─ /auth/google/* → identity
orchestrator ── RabbitMQ ── identity · institutions · grades_ingest · grades_query · reviews · notifications
```

| Service | Owns | Notes |
|---|---|---|
| `orchestrator` | nothing (stateless) | The public API: authentication, authorisation, rate limits, and the grade-publishing saga (check → charge → confirm → forward). |
| `identity` | users, student registry, account tokens | The only JWT issuer. Password and Google sign-in. |
| `institutions` | institutions, credit balances and ledger | Idempotent charges (`charge:<grading>`). |
| `grades_ingest` | uploads, gradings, grades (with names) | Parses workbooks; state machine none → open → final. The source of truth for grades. |
| `grades_query` | personal grades, precomputed distributions | Read side, synchronised through the orchestrator and reconciled periodically. Serves the exam-period read peaks. |
| `reviews` | review requests, grading headers | |
| `notifications` | email outbox | Retries with backoff and an hourly budget. |

Every service is written in Go 1.26, owns its own PostgreSQL 17 database
(none is shared) and uses the shared `contracts` module: the RabbitMQ RPC
envelope, queue topology, message types, deterministic IDs, logging and
tracing. Details: [docs/data-model.md](docs/data-model.md),
[docs/rabbitmq-topology.md](docs/rabbitmq-topology.md),
[docs/service-contracts.md](docs/service-contracts.md),
[docs/openapi.yaml](docs/openapi.yaml) and the plan in
[docs/roadmap.md](docs/roadmap.md).

---

## Documentation

- **SRS:** `clearSKY-SRS.pdf` defines the functional and non-functional requirements.
- **UML & Design:** Visual Paradigm project in `/architecture` (original design).
- **Operations:** [docs/auth-cutover.md](docs/auth-cutover.md) (secrets, first administrator, HTTPS), [docs/account-onboarding.md](docs/account-onboarding.md), [docs/observability.md](docs/observability.md).

---

## Technology Stack

- **Languages:** Go 1.26 (services), Node.js 22 (front-end)
- **Data:** PostgreSQL 17 (one per service), RabbitMQ 3.13
- **Web:** Caddy (HTTPS entry point), Gin, Express/EJS
- **Observability:** structured JSON logs with trace IDs, OpenTelemetry → Collector → Jaeger
- **Containers:** Docker Compose

---

## Directory Structure

```
/
├── contracts/               # shared Go module: RPC envelope, topology, messages, app runner
├── orchestrator/            # API gateway
├── identity_service/        # accounts, JWT, Google sign-in
├── institutions_service/    # institutions and credits
├── grades_ingest_service/   # workbook upload and gradings (write side)
├── grades_query_service/    # personal grades and statistics (read side)
├── reviews_service/         # review requests
├── notifications_service/   # email outbox
├── front-end/               # Express/EJS UI
├── deploy/                  # Caddyfile, OpenTelemetry Collector configuration
├── tools/                   # seed data generator, end-to-end tests
├── tests/                   # repository checks, legacy characterization
├── docs/                    # design, operations, roadmap
└── docker-compose.yml       # the whole stack
```

---

## How to Run

### 1. Prerequisites

- **Docker** with **Docker Compose v2**; ports 80 and 443 free (or set `HTTP_PORT` / `HTTPS_PORT`)
- For development: **Go 1.26**, **Node.js 22**, **Python 3.10+** with PyYAML

### 2. Configuration

Copy `.env.example` to `.env` and fill in every required value (Compose
refuses to start otherwise): `JWT_SECRET`, `SESSION_SECRET`, the RabbitMQ
account, one password per database, and a bootstrap secretariat account for a
new installation. Generate each secret with `openssl rand -hex 32`. See
[docs/auth-cutover.md](docs/auth-cutover.md).

### 3. Build & Launch

```bash
docker compose up --build -d
docker compose ps          # every service reports (healthy)
```

### 4. Access Points

| What | Where |
|---|---|
| Application | <https://localhost> (the API is under `/api`) |
| Mailpit, the development email inbox | <http://127.0.0.1:8025> |
| Jaeger, traces | <http://127.0.0.1:16686> |

Caddy serves `localhost` with its own local certificate authority. Browsers
warn until you trust it once:

```bash
docker compose cp proxy:/data/caddy/pki/authorities/local/root.crt clearsky-local-ca.crt
# then import clearsky-local-ca.crt into the browser or OS trust store
```

For a public domain set `SITE_ADDRESS` and `PUBLIC_APP_URL`; Caddy then
obtains a certificate automatically.

### 5. Demo data

`tools/cmd/seed` creates a complete, realistic data set through the public API
(the institution, credits, the student registry, instructors and students
activated from the emailed links, open and final gradings, review requests
in every state). It is deterministic and can be re-run.

```bash
set -a; . ./.env; set +a
cd tools && go run ./cmd/seed
```

All seeded accounts share one password (`SEED_PASSWORD`, or a random one that
the tool prints).

### 6. Tests

```bash
python3 -m unittest discover -s tests                 # repository and Compose checks
(cd contracts && go test ./...)                       # likewise in every service directory;
                                                      # needs TEST_DATABASE_URL and TEST_AMQP_URL
set -a; . ./.env; set +a
(cd tools && go test -tags e2e -count=1 ./e2e)        # end to end against the running stack,
                                                      # including failure tests (stops containers)
```

The Go tests use a real PostgreSQL and RabbitMQ, for example:

```bash
docker run -d --name clearsky-testdb -e POSTGRES_PASSWORD=test -p 127.0.0.1:55432:5432 postgres:17.11
docker run -d --name clearsky-testmq -p 127.0.0.1:55672:5672 rabbitmq:3.13.7-management
export TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable
export TEST_AMQP_URL=amqp://guest:guest@127.0.0.1:55672/
```

### 7. Databases

No database, and not RabbitMQ, publishes a host port. For temporary local
access copy `docker-compose.override.example.yml` to
`docker-compose.override.yml` (ports bind to `127.0.0.1` only), or use the
container's client, e.g. `docker compose exec grades_query_db psql -U grades_query`.

The `backup` service dumps the databases that are a source of truth
(identity, institutions, grades_ingest, reviews) every day into the `backups`
volume and keeps the newest seven; grades_query needs no backup because it is
rebuilt from grades_ingest. To check that the newest backups restore:

```bash
docker compose exec backup sh /scripts/restore-check.sh
```

### 8. Stopping & Cleaning

```bash
docker compose down        # stop
docker compose down -v     # also delete every database, the backups and the broker's data
```

---

## Development & Troubleshooting

- Logs of one service: `docker compose logs -f <service>` (JSON, one line per event, with `trace_id`).
- Rebuild one service after a change: `docker compose up --build -d <service>`.
- Follow a request across services: open it in Jaeger by its `trace_id`.
- A service that is not `(healthy)`: `docker compose logs <service>`; readiness is `/health/ready` on port 8080 (database and broker checks).
- No account exists after the first start: set `BOOTSTRAP_ADMIN_USERNAME` / `BOOTSTRAP_ADMIN_PASSWORD` and restart `identity`.

---

## Why This Project Stands Out

- **Enterprise-Ready:** Clean microservices separation, message-driven, and scalable.
- **Formal Documentation:** SRS and UML artifacts (VPP) for professional maintainability.
- **Full DevOps Pipeline:** Dockerized, with easy local and cloud deployment.
- **Modern Stack:** Uses current best practices in Go, Node.js, and cloud-native design.
- **Portfolio-Grade:** Demonstrates advanced backend, distributed systems, and system design skills.

---

## Authors

- Team 12, NTUA ECE, Software as a Service Technologies (2024–2025)
- See each service's README for contributors.
- Anastasiadis Vassilis, Gratsia Maria, Thivaios Dimitris, Liakis Dimitris, Mitropoulos Stavros (in alphabetical order)

---

## My Contribution
- I had significant contribution to this project mainly focused on Microservices Development, Orchestration & Architecture & Deployment,.

- ![image](https://github.com/user-attachments/assets/4cd8b2d5-977b-41ee-a00a-8ee566c3cc12)
 

## License

MIT (see [LICENSE](LICENSE))

# Clear Sky SaaS Platform

**NTUA ECE SAAS 2025 – Team 12**

---

## Project Overview

Clear Sky is a modular, production-grade SaaS platform for academic institutions, designed to manage student grades, review workflows, user authentication, institutional credits, and more. The system is architected as a set of loosely coupled microservices, communicating asynchronously via RabbitMQ, and is fully containerized for easy deployment and scalability.

This project was developed following a formal Software Requirements Specification (SRS) and is accompanied by comprehensive UML documentation (Visual Paradigm Project, VPP), ensuring maintainability, clarity, and extensibility.

---

## Key Features

- **User Authentication & Authorization:** Secure login (classic & Google OAuth2), JWT-based session management, and role-based access control.
- **Grade Management:** Instructors can upload grades via Excel; students can view personal grades and statistics.
- **Review Workflow:** Students submit grade review requests; instructors reply and manage review status.
- **Institutional Credits:** Institutions manage and purchase credits for grade submissions.
- **Statistics & Analytics:** Dynamic grade histograms and performance analytics for courses.
- **Admin & Registration:** Institution onboarding and user management.
- **Modern Web UI:** Responsive front-end with role-based dashboards.

---

## Architecture

The platform is composed of the following microservices:

- **Orchestrator:** API gateway and message router (Go, Gin)
- **User Management Service:** Registration, login, JWT, user roles (Go)
- **Google Auth Service:** Google OAuth2 login, JWT issuance (Go)
- **Credits Service:** Institution credits management (Go, PostgreSQL)
- **Registration Service:** Institution onboarding (Go, PostgreSQL)
- **Initial/Final Grades Services:** Grade import from Excel (Node.js, MongoDB)
- **Stats Service:** Grade statistics and analytics (Node.js, MySQL)
- **View Grades Service:** Student grade viewing (Node.js, MySQL)
- **Student Request Review Service:** Student review requests (Go, PostgreSQL)
- **Instructor Review Reply Service:** Instructor responses (Go, PostgreSQL)
- **Front-end:** Express/EJS web UI (Node.js)

All services communicate via RabbitMQ (`clearSky.events` exchange). The system is orchestrated using Docker Compose for seamless multi-service deployment.

---

## Documentation

- **SRS:** The project strictly follows a detailed Software Requirements Specification (SRS) which defines all functional and non-functional requirements.
- **UML & Design:** Full UML diagrams (class, sequence, deployment, etc.) are available in Visual Paradigm Project (VPP) format in the `/architecture` folder.
- **API Contracts:** Each service includes its own README with message formats and endpoint documentation.

---

## Technology Stack

- **Languages:** Go 1.24+, Node.js 18+/20+
- **Databases:** PostgreSQL, MySQL, MongoDB, SQLite (for some auth)
- **Messaging:** RabbitMQ (direct exchange)
- **Web:** Express.js, EJS, Gin
- **Containerization:** Docker, Docker Compose
- **Auth:** JWT, Google OAuth2
- **Testing & Tooling:** Visual Paradigm (UML), Postman, npm, go modules

---

## Directory Structure

```
/
├── orchestrator/                  # API gateway and message router
├── user_management_service/       # Auth, JWT, user DB
├── google_auth_service/           # Google OAuth2 login
├── credits_service/               # Institution credits (Postgres)
├── registration_service/          # Institution registration (Postgres)
├── initial_grades/                # Initial grade import (MongoDB)
├── final_grades/                  # Final grade import (MongoDB)
├── stats_service/                 # Grade statistics (MySQL)
├── View_personal_grades/          # Student grade viewing (MySQL)
├── student_request_review_service/# Student review requests (Postgres)
├── instructor_review_reply_service/# Instructor review replies (Postgres)
├── front-end/                     # Express/EJS UI
├── architecture/                  # UML, SRS, VPP documentation
├── docker-compose.yml             # Main Compose file (all services)
└── ...
```

---

## How to Run

Be sure that the necessary ports are free.
For google auth, you need to set yourself the .env.

### 1. Prerequisites

- **Docker** and **Docker Compose v2+**
- (Optional for local development) **Go 1.24+** and **Node.js 18+**

### 2. Configuration

- Each microservice requires its own `.env` file.  
  Copy `.env.example` or create a `.env` in each service directory.
- Set environment variables for database connections, RabbitMQ, Google OAuth, and JWT secrets.
- For Google Auth: set `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL`, `INTERNAL_AUTH_TOKEN`.
- The root stack requires `JWT_SECRET`, `INTERNAL_AUTH_TOKEN` and `SESSION_SECRET` (32+ characters each)
  and the RabbitMQ account `RABBITMQ_DEFAULT_USER` / `RABBITMQ_DEFAULT_PASS`.
  Copy the root `.env.example` to `.env`; see [docs/auth-cutover.md](docs/auth-cutover.md).
- Google login needs `GOOGLE_ALLOWED_DOMAINS` (the university email domains). Student and instructor
  onboarding is described in [docs/account-onboarding.md](docs/account-onboarding.md).

### 3. Build & Launch

```bash
docker compose down -v --remove-orphans
docker compose up --build -d
```

- Check that all containers are running:
  ```bash
  docker compose ps
  ```
- View logs:
  ```bash
  docker compose logs -f
  ```

### 4. Access Points

- **Front-end Web UI:** [http://localhost:3000](http://localhost:3000)
- **Orchestrator API:** [http://localhost:8080](http://localhost:8080)
- **Mailpit (development email inbox):** [http://127.0.0.1:8025](http://127.0.0.1:8025)
- **RabbitMQ:** internal to the Docker network only; see [docs/auth-cutover.md](docs/auth-cutover.md) for temporary UI access

### 5. Database Ports

- **PostgreSQL:** 5440–5443 (various services)
- **MySQL:** 3306–3307
- **MongoDB:** 27017–27018

### 6. Stopping & Cleaning

- Stop all services:
  ```bash
  docker compose down
  ```
- Remove all containers, networks, and volumes:
  ```bash
  docker compose down -v --remove-orphans
  ```

---

## Development & Troubleshooting

- Inspect logs for a specific service:
  ```bash
  docker compose logs -f <service>
  ```
- Restart a service after code changes:
  ```bash
  docker compose up --build -d <service>
  ```
- Database access:  
  Use `psql`, `mysql`, or `mongo` CLI tools to connect to the respective DB containers.
- Initial user: none is created automatically. Set `BOOTSTRAP_ADMIN_USERNAME` and
  `BOOTSTRAP_ADMIN_PASSWORD` (12+ characters) before the first start to create an
  institution representative. See [docs/auth-cutover.md](docs/auth-cutover.md).

- Common issues:
  - **"relation ... does not exist"**: DB init script did not run. Remove volumes and restart.
  - **Connection refused**: Check service health and correct host/port.
  - **Service restart loops**: Inspect logs for missing environment variables or misconfiguration.

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

MIT (see individual service folders for details)

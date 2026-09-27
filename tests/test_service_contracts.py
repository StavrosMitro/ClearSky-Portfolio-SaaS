"""Static cross-service guards (Compose wiring, secrets, auth boundaries).

These do not contact live services; tests/e2e does. Needs PyYAML.
"""
from pathlib import Path
import re
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
COMPOSE_TEXT = (ROOT / "docker-compose.yml").read_text()
COMPOSE = yaml.safe_load(COMPOSE_TEXT)
SERVICES = COMPOSE["services"]
# Compose service → Go module directory; each owns the database <service>_db.
OWNERS = {
    "identity": "identity_service",
    "institutions": "institutions_service",
    "grades_ingest": "grades_ingest_service",
    "grades_query": "grades_query_service",
    "reviews": "reviews_service",
    "notifications": "notifications_service",
}


def env(service):
    return SERVICES[service].get("environment", {})


def required(variable):
    return f"${{{variable}:?"


class ComposeTests(unittest.TestCase):
    def test_topology(self):
        expected = {"proxy", "frontend", "orchestrator", "rabbitmq", "mailpit", "otel-collector", "jaeger", "backup",
                    *OWNERS, *(f"{s}_db" for s in OWNERS)}
        self.assertEqual(set(SERVICES), expected)
        stale = [str(p.relative_to(ROOT)) for p in ROOT.glob("*/docker-compose*.yml")]
        self.assertEqual(stale, [], "services run only from the root docker-compose.yml")

    def test_only_the_proxy_is_public(self):
        for name, service in SERVICES.items():
            for port in service.get("ports", []):
                if name != "proxy":
                    self.assertTrue(str(port).startswith("127.0.0.1:"), f"{name} publishes {port}")
        self.assertNotIn("expose", SERVICES["rabbitmq"])

    def test_each_service_owns_its_database(self):
        for service, module in OWNERS.items():
            db = f"{service}_db"
            self.assertEqual(SERVICES[db]["image"], "postgres:17.11")
            self.assertIn(required(f"{service.upper()}_DB_PASSWORD"), SERVICES[db]["environment"]["POSTGRES_PASSWORD"])
            url = env(service)["DATABASE_URL"]
            self.assertRegex(url, rf"^postgres://{service}:\$\{{{service.upper()}_DB_PASSWORD\}}@{db}:5432/{service}\?")
            self.assertEqual(SERVICES[service]["depends_on"][db]["condition"], "service_healthy")
            self.assertEqual(SERVICES[service]["build"]["dockerfile"], f"{module}/Dockerfile")
        # No other container can reach a database through its URL.
        for name in ("orchestrator", "frontend"):
            self.assertNotIn("DATABASE_URL", env(name))

    def test_broker_credentials_are_required_and_shared(self):
        rabbitmq = SERVICES["rabbitmq"]["environment"]
        self.assertIn(required("RABBITMQ_DEFAULT_USER"), rabbitmq["RABBITMQ_DEFAULT_USER"])
        self.assertIn(required("RABBITMQ_DEFAULT_PASS"), rabbitmq["RABBITMQ_DEFAULT_PASS"])
        self.assertNotIn("guest", COMPOSE_TEXT)
        for name in ("orchestrator", *OWNERS):
            self.assertRegex(env(name)["AMQP_URL"], r"^amqp://\$\{RABBITMQ_DEFAULT_USER:\?[^}]*\}:\$\{RABBITMQ_DEFAULT_PASS:\?[^}]*\}@rabbitmq:5672/$")

    def test_signing_keys_reach_only_their_owners(self):
        holders = {name for name in SERVICES if "JWT_SECRET" in env(name)}
        self.assertEqual(holders, {"identity", "orchestrator"})
        for variable in ("JWT_ISSUER", "JWT_AUDIENCE"):
            self.assertEqual(env("identity")[variable], env("orchestrator")[variable])
        self.assertIn(required("JWT_SECRET"), env("orchestrator")["JWT_SECRET"])
        self.assertIn(required("SESSION_SECRET"), env("frontend")["SESSION_SECRET"])
        self.assertNotIn("INTERNAL_AUTH_TOKEN", COMPOSE_TEXT)
        self.assertNotIn("env_file", COMPOSE_TEXT)
        self.assertNotRegex(COMPOSE_TEXT, r"(?i)(PASSWORD|PASS)[:=]\s*(root|2002|password|guest)\b")

    def test_proxy_routes(self):
        caddyfile = (ROOT / "deploy/caddy/Caddyfile").read_text()
        self.assertRegex(caddyfile, r"handle_path /api/\* \{\s*reverse_proxy orchestrator:8080")
        self.assertRegex(caddyfile, r"handle /auth/google/\* \{\s*reverse_proxy identity:8080")
        self.assertRegex(caddyfile, r"handle \{\s*reverse_proxy frontend:3000")
        self.assertEqual(env("orchestrator")["TRUSTED_PROXIES"], "${TRUSTED_PROXIES:-proxy}")
        self.assertTrue(env("identity")["GOOGLE_REDIRECT_URL"].endswith("/auth/google/callback"))

    def test_sources_of_truth_are_backed_up(self):
        backup = SERVICES["backup"]
        script = (ROOT / "deploy/backup/backup.sh").read_text()
        self.assertIn('DATABASES="identity institutions grades_ingest reviews"', script)
        for service in ("identity", "institutions", "grades_ingest", "reviews"):
            self.assertIn(f"{service.upper()}_DB_PASSWORD", backup["environment"])
        self.assertNotIn("GRADES_QUERY_DB_PASSWORD", backup["environment"], "grades_query is rebuilt by reconcile")
        self.assertIn("backups:/backups", backup["volumes"])

    def test_every_service_has_a_readiness_check(self):
        for name in ("frontend", "orchestrator", *OWNERS):
            self.assertIn("healthcheck", SERVICES[name], name)


class SourceTests(unittest.TestCase):
    def go_sources(self, module):
        return [p for p in (ROOT / module).rglob("*.go") if not p.name.endswith("_test.go")]

    def test_services_share_the_contracts_module(self):
        for module in ("orchestrator", *OWNERS.values()):
            gomod = (ROOT / module / "go.mod").read_text()
            self.assertIn("replace clearsky/contracts => ../contracts", gomod, module)
            self.assertRegex(gomod, r"(?m)^go 1\.26", module)

    def test_gateway_handlers_use_the_shared_response_writer(self):
        for path in (ROOT / "orchestrator/internal/handlers").glob("*.go"):
            if not path.name.endswith("_test.go"):
                self.assertNotIn("c.JSON(", path.read_text(), path.name)

    def test_identity_is_the_only_jwt_issuer(self):
        issuers = [str(p.relative_to(ROOT)) for m in ("orchestrator", *OWNERS.values())
                   for p in self.go_sources(m) if "SignedString(" in p.read_text()]
        self.assertEqual(issuers, ["identity_service/pkg/jwt/token.go"])
        issuer = (ROOT / "identity_service/pkg/jwt/token.go").read_text()
        for claim in ("Issuer:", "Subject:", "Audience:", "IssuedAt:", "ExpiresAt:", "ID:", "InstitutionID"):
            self.assertIn(claim, issuer)

    def test_frontend_does_not_persist_or_log_tokens(self):
        source = "\n".join(p.read_text(errors="ignore") for p in (ROOT / "front-end").rglob("*.js")
                           if "node_modules" not in p.parts)
        self.assertNotRegex(source, r"localStorage\.(?:setItem|getItem)\(['\"]jwt")
        self.assertNotIn("document.cookie.match", source)

    def test_google_access_is_domain_based(self):
        for path in self.go_sources("identity_service/internal/google"):
            self.assertNotRegex(path.read_text(), r"@[a-z0-9.-]+\.[a-z]{2,}\"\s*:\s*true", path.name)
        self.assertIn("GOOGLE_ALLOWED_DOMAINS", env("identity"))

    def test_students_cannot_self_assign_a_student_id(self):
        consumer = (ROOT / "identity_service/internal/messaging/consume.go").read_text()
        self.assertNotIn('case "register":', consumer)
        self.assertIn('case "request_student_activation":', consumer)
        self.assertIn("errRosterMismatch", (ROOT / "identity_service/internal/accounts/accounts.go").read_text())

    def test_no_default_accounts_or_hard_coded_passwords(self):
        for path in self.go_sources("identity_service"):
            self.assertNotRegex(path.read_text(), r'GenerateFromPassword\(\s*\[\]byte\(\s*"', path.name)

    def test_grade_snapshots_to_the_read_side_carry_no_names(self):
        grades = (ROOT / "orchestrator/internal/handlers/grades.go").read_text()
        self.assertIn("StudentName = \"\"", grades.replace("\t", " "))


if __name__ == "__main__":
    unittest.main()

"""Static cross-language contract guards; these do not contact live services."""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]


def compose_service_blocks(relative):
    """Return {service: text} for a Compose file without a YAML dependency."""
    blocks, current, in_services = {}, None, False
    for line in (ROOT / relative).read_text().splitlines():
        if line.strip() and not line.startswith((" ", "#")):
            in_services, current = line.rstrip() == "services:", None
            continue
        match = re.match(r"^  ([A-Za-z0-9_.-]+):\s*$", line)
        if in_services and match:
            current = match.group(1)
            blocks[current] = []
        elif in_services and current:
            blocks[current].append(line)
    return {name: "\n".join(lines) for name, lines in blocks.items()}


def required(variable):
    return f"${{{variable}:?"


class ServiceContractTests(unittest.TestCase):
    def test_rpc_producers_use_v1_and_not_status_strings(self):
        producers = [
            "initial_grades/app.js",
            "final_grades/app.js",
            "stats_service/app.js",
            "View_personal_grades/app.js",
            "credits_service/handlers/rpc.go",
            "registration_service/handlers/HandlerRegister.go",
            "student_request_review_service/mq/consumer.go",
            "instructor_review_reply_service/mq/consumer.go",
            "user_management_service/internal/messaging/consume.go",
            "google_auth_service/rabbitmq/consumer.go",
        ]
        legacy = re.compile(r"(?:status\s*:\s*['\"](?:ok|error|conflict)|Status:\s*['\"](?:ok|error|conflict))", re.I)
        for relative in producers:
            source = (ROOT / relative).read_text()
            self.assertRegex(source, r"(?:version\s*:\s*1|Version:\s*1|rpcVersion\s*=\s*1)", relative)
            self.assertIsNone(legacy.search(source), relative)

    def test_orchestrator_handlers_use_shared_http_response_writer(self):
        for path in (ROOT / "orchestrator/internal/handlers").glob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            self.assertNotIn("c.JSON(", path.read_text(), str(path))

    def test_user_management_is_the_only_application_jwt_issuer(self):
        google = "\n".join(
            path.read_text(errors="ignore")
            for path in (ROOT / "google_auth_service").rglob("*.go")
        )
        self.assertNotIn("SignedString(", google)
        issuer = (ROOT / "user_management_service/pkg/jwt/token.go").read_text()
        self.assertIn("SignedString(", issuer)
        for claim in ("Issuer:", "Subject:", "Audience:", "IssuedAt:", "ExpiresAt:", "ID:"):
            self.assertIn(claim, issuer)

    def test_frontend_does_not_persist_or_log_tokens(self):
        source = "\n".join(
            path.read_text(errors="ignore")
            for path in (ROOT / "front-end").rglob("*.js")
            if "node_modules" not in path.parts
        )
        self.assertNotRegex(source, r"localStorage\.(?:setItem|getItem)\(['\"]jwt")
        self.assertNotIn("document.cookie.match", source)

    def test_compose_wires_auth_secrets_and_keeps_user_management_internal(self):
        services = compose_service_blocks("docker-compose.yml")
        google = services["google_auth_service"]
        user_management = services["user_management_service"]
        orchestrator = services["orchestrator"]

        self.assertNotIn("JWT_SECRET", google, "Google auth must not hold the application signing key")
        self.assertIn(required("INTERNAL_AUTH_TOKEN"), google)
        self.assertIn(required("INTERNAL_AUTH_TOKEN"), user_management)
        self.assertIn(required("SESSION_SECRET"), services["frontend"])
        self.assertNotIn("ports:", user_management)
        self.assertIn("expose:", user_management)
        for variable in ("JWT_SECRET", "JWT_ISSUER", "JWT_AUDIENCE"):
            for name, block in (("orchestrator", orchestrator), ("user_management_service", user_management)):
                self.assertIn(f"{variable}=${{{variable}:", block, f"{name} must receive {variable}")
        self.assertIn(required("JWT_SECRET"), orchestrator)
        self.assertIn(required("JWT_SECRET"), user_management)

        standalone = compose_service_blocks("user_management_service/docker-compose.yml")["auth-service"]
        self.assertNotIn("ports:", standalone)

    def test_rabbitmq_is_internal_and_uses_configured_credentials(self):
        compose = (ROOT / "docker-compose.yml").read_text()
        rabbitmq = compose_service_blocks("docker-compose.yml")["rabbitmq"]
        self.assertNotIn("ports:", rabbitmq, "RabbitMQ must not publish host ports")
        self.assertIn(required("RABBITMQ_DEFAULT_USER"), rabbitmq)
        self.assertIn(required("RABBITMQ_DEFAULT_PASS"), rabbitmq)
        self.assertNotIn("guest", compose)
        for url in re.findall(r"amqp://[^\s\"']*", compose):
            self.assertTrue(
                url.startswith("amqp://${RABBITMQ_DEFAULT_USER}:${RABBITMQ_DEFAULT_PASS}@rabbitmq:5672"),
                url,
            )
        for service in ("student_request_review_service", "instructor_review_reply_service"):
            source = (ROOT / service / "mq/initmq.go").read_text()
            self.assertIn('os.Getenv("AMQP_URL")', source, service)

    def test_google_access_is_domain_based_not_a_hardcoded_allowlist(self):
        for path in (ROOT / "google_auth_service").rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            self.assertNotRegex(path.read_text(), r"@[a-z0-9.-]+\.[a-z]{2,}\"\s*:\s*true", str(path))
        google = compose_service_blocks("docker-compose.yml")["google_auth_service"]
        self.assertIn("GOOGLE_ALLOWED_DOMAINS=", google)

    def test_students_cannot_self_assign_a_student_id(self):
        consumer = (ROOT / "user_management_service/internal/messaging/consume.go").read_text()
        self.assertNotIn('case "register":', consumer)
        self.assertIn('case "request_student_activation":', consumer)
        onboarding = (ROOT / "user_management_service/internal/accounts/accounts.go").read_text()
        self.assertIn("errRosterMismatch", onboarding)

    def test_user_management_has_no_public_password_routes_or_default_accounts(self):
        main = (ROOT / "user_management_service/cmd/server/main.go").read_text()
        for route in ('"/login"', '"/register"', '"/upsert"'):
            self.assertNotIn(route, main)
        for path in (ROOT / "user_management_service").rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            self.assertNotRegex(
                path.read_text(),
                r'GenerateFromPassword\(\s*\[\]byte\(\s*"',
                f"{path} hashes a hard-coded password",
            )


if __name__ == "__main__":
    unittest.main()

"""Static RabbitMQ topology guards; run `python3 -m unittest discover tests`.

The live behaviour (declaration, dead-lettering, RPC) is tested in Go against
a real broker (contracts/amqpx) and end to end in tests/e2e.
"""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
TOPOLOGY = (ROOT / "contracts/topology/topology.go").read_text()
SERVICES = ["identity_service", "institutions_service", "grades_ingest_service",
            "grades_query_service", "reviews_service", "notifications_service"]


def owned_queues():
    """{Go variable: (queue name, [routing keys])} from contracts/topology."""
    queues = {}
    for var, name, keys in re.findall(r'^\t(\w+)\s*=\s*Queue\{"([^"]+)", \[\]string\{([^}]*)\}\}', TOPOLOGY, re.M):
        queues[var] = (name, re.findall(r'"([^"]+)"', keys))
    return queues


def active_go_sources():
    for path in ROOT.rglob("*.go"):
        if not path.name.endswith("_test.go") and not {".git", "node_modules"} & set(path.parts):
            yield path


class TopologyTests(unittest.TestCase):
    def test_exchanges(self):
        for name, kind in {"clearsky.commands.v1": "direct", "clearsky.events.v1": "topic",
                           "clearsky.dlx.v1": "direct"}.items():
            self.assertRegex(TOPOLOGY, rf'"{re.escape(name)}"\s*//\s*{kind}')

    def test_each_routing_key_has_exactly_one_queue(self):
        queues = owned_queues()
        self.assertEqual(len(queues), 8, queues)
        owners = {}
        for name, keys in queues.values():
            self.assertTrue(name.startswith("clearsky.") and name.endswith(".v1"), name)
            for key in keys:
                owners.setdefault(key, []).append(name)
        self.assertTrue(all(len(q) == 1 for q in owners.values()), owners)

    def test_each_queue_is_consumed_by_exactly_one_service(self):
        consumers = {}
        for service in SERVICES:
            for main in (ROOT / service / "cmd").rglob("main.go"):
                for var in re.findall(r"amqpx\.NewServer\([^,]+,\s*topology\.(\w+),", main.read_text()):
                    consumers.setdefault(var, []).append(service)
        self.assertEqual(sorted(consumers), sorted(owned_queues()))
        self.assertTrue(all(len(s) == 1 for s in consumers.values()), consumers)

    def test_sync_queues_have_a_single_ordered_consumer(self):
        # Snapshots of one grading must be applied in order (version check).
        for service, var in (("grades_query_service", "GradesSync"), ("reviews_service", "ReviewsSync")):
            main = next((ROOT / service / "cmd").rglob("main.go")).read_text()
            self.assertRegex(main, rf"topology\.{var},\s*1,", service)

    def test_queues_are_declared_only_through_contracts(self):
        for path in active_go_sources():
            if path.is_relative_to(ROOT / "contracts") or path == ROOT / "orchestrator/internal/rabbitmq/client.go":
                continue
            source = path.read_text()
            for forbidden in ("QueueDeclare(", "ExchangeDeclare(", "QueueBind("):
                self.assertNotIn(forbidden, source, f"{path.relative_to(ROOT)}: {forbidden}")

    def test_gateway_handlers_only_call_through_the_client(self):
        for path in (ROOT / "orchestrator/internal/handlers").glob("*.go"):
            source = path.read_text()
            for forbidden in ("QueueDeclare(", ".Consume(", ".ConsumeWithContext(", ".Publish("):
                self.assertNotIn(forbidden, source, f"{path.name}: {forbidden}")

    def test_gateway_publishes_persistent_confirmed_mandatory(self):
        source = (ROOT / "orchestrator/internal/rabbitmq/client.go").read_text()
        self.assertRegex(source, r"DeliveryMode:\s+amqp\.Persistent")
        self.assertIn("PublishWithDeferredConfirm", source)
        self.assertRegex(source, r"routingKey,\s+true,\s+false,")  # mandatory=true, immediate=false
        self.assertIn("Confirm(false)", source)

    def test_retired_routes_are_gone(self):
        retired = ["postgrades.", "credits.avail", "stats.avail", "view.avail", "student.postNewRequest",
                   "instructor.postResponse", "auth.login.google", "user.created", "clearsky.auth.commands.v1"]
        for path in active_go_sources():
            source = path.read_text()
            for key in retired:
                self.assertNotIn(f'"{key}', source, f"{path.relative_to(ROOT)}: {key}")


if __name__ == "__main__":
    unittest.main()

"""Deterministic topology checks; run `python3 -m unittest tests/test_rabbitmq_topology.py`.
A live-broker test is intentionally not claimed here: it requires an isolated RabbitMQ instance.
"""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
EXCHANGES = {"clearsky.commands.v1": "direct", "clearsky.events.v1": "topic", "clearsky.dlx.v1": "direct"}
COMMANDS = {
 "clearsky.auth.commands.v1": ["auth.request"],
 "clearsky.google-auth.commands.v1": ["auth.login.google"],
 "clearsky.credits.commands.v1": ["credits.avail", "credits.spent", "credits.purchased", "add.new"],
 "clearsky.registration.commands.v1": ["institution.registered"],
 "clearsky.student-review.commands.v1": ["student.postNewRequest", "student.getRequestStatus", "student.updateInstructorResponse"],
 "clearsky.instructor-review.commands.v1": ["instructor.postResponse", "instructor.getRequestsList", "instructor.getRequestInfo", "instructor.insertStudentRequest", "instructor.addCourse"],
 "clearsky.initial-grades.commands.v1": ["postgrades.init"],
 "clearsky.final-grades.commands.v1": ["postgrades.final", "incr.credits"],
 "clearsky.stats.commands.v1": ["postgrades.statistics", "stats.avail", "stats.get"],
 "clearsky.personal-grades.commands.v1": ["postgrades.view", "view.avail"],
}
SOURCES = [*COMMANDS, "clearsky.orchestrator.events.v1"]

class TopologyTests(unittest.TestCase):
 def test_canonical_exchange_contract(self):
  self.assertEqual(EXCHANGES, {"clearsky.commands.v1":"direct", "clearsky.events.v1":"topic", "clearsky.dlx.v1":"direct"})
 def test_each_command_has_exactly_one_owner(self):
  owners = {}
  for queue, keys in COMMANDS.items():
   for key in keys: owners.setdefault(key, []).append(queue)
  self.assertTrue(all(len(queues) == 1 for queues in owners.values()), owners)
 def test_grade_routes_do_not_compete(self):
  self.assertNotIn("postgrades.statistics", COMMANDS["clearsky.personal-grades.commands.v1"])
  self.assertNotIn("postgrades.view", COMMANDS["clearsky.stats.commands.v1"])
  self.assertEqual(COMMANDS["clearsky.initial-grades.commands.v1"], ["postgrades.init"])
  self.assertIn("postgrades.final", COMMANDS["clearsky.final-grades.commands.v1"])
 def test_dead_letter_contract(self):
  for source in SOURCES:
   self.assertEqual(f"{source}.dlq", source + ".dlq")
   self.assertEqual(f"{source}.dead", source + ".dead")
 def test_orchestrator_event_binding_is_handled_only(self):
  config = (ROOT / "orchestrator/configs/config.dev.yaml").read_text()
  self.assertIn('  - "user.created"', config)
  self.assertNotIn('  - "user.login.google"', config)
 def test_active_source_has_no_legacy_event_declarations_or_shared_grade_queue(self):
  excluded = {ROOT / "docs", ROOT / ".git", ROOT / "node_modules"}
  files = [p for p in ROOT.rglob('*') if p.is_file() and p.suffix in {'.go','.js','.yml','.yaml'} and not any(part in {'.git', 'node_modules', 'docs'} for part in p.parts)]
  text = "\n".join(p.read_text(errors="ignore") for p in files)
  self.assertNotIn('ExchangeDeclare("clearSky.events"', text)
  self.assertNotIn('ExchangeDeclare("clearsky.events"', text)
  self.assertNotIn("assertExchange(RABBITMQ_EXCHANGE, 'topic'", text)
  self.assertNotIn("const q = 'postgrades.final'", text)
 def test_application_declarations_include_owned_queue_and_dlx_binding(self):
  declarations = {
   "clearsky.auth.commands.v1": "user_management_service/internal/messaging/rabbit.go",
   "clearsky.google-auth.commands.v1": "google_auth_service/rabbitmq/consumer.go",
   "clearsky.credits.commands.v1": "credits_service/main.go",
   "clearsky.registration.commands.v1": "registration_service/main.go",
   "clearsky.student-review.commands.v1": "student_request_review_service/mq/consumer.go",
   "clearsky.instructor-review.commands.v1": "instructor_review_reply_service/mq/consumer.go",
   "clearsky.initial-grades.commands.v1": "initial_grades/app.js",
   "clearsky.final-grades.commands.v1": "final_grades/app.js",
   "clearsky.stats.commands.v1": "stats_service/app.js",
   "clearsky.personal-grades.commands.v1": "View_personal_grades/app.js",
  }
  for queue, file in declarations.items():
   source = (ROOT / file).read_text()
   self.assertIn(queue, source, file)
   self.assertIn("clearsky.dlx.v1", source, file)
   self.assertTrue(queue + ".dlq" in source or 'queue+".dlq"' in source or 'queueName+".dlq"' in source, file)
 def test_orchestrator_client_owns_confirmed_persistent_publishing(self):
  source = (ROOT / "orchestrator/internal/rabbitmq/client.go").read_text()
  self.assertIn('"clearsky.commands.v1"', source)
  self.assertRegex(source, r'DeliveryMode:\s+amqp\.Persistent')
  self.assertIn('PublishWithDeferredConfirm', source)
  self.assertRegex(source, r'routingKey,\s+true,\s+false,')  # mandatory=true, immediate=false
  self.assertIn('Confirm(false)', source)

 def test_google_event_is_named_and_persistent(self):
  text = (ROOT / "google_auth_service/rabbitmq/publisher.go").read_text()
  self.assertIn('"user.login.google"', text)
  self.assertIn('DeliveryMode: amqp.Persistent', text)

 def test_retired_queues_and_handler_local_rpc_are_absent(self):
  for file, retired in {
   "stats_service/app.js": ["get.submission.logs", "get.grades"],
   "View_personal_grades/app.js": ["grades.get.byAM.q"],
  }.items():
   source = (ROOT / file).read_text()
   for queue in retired: self.assertNotIn(queue, source)
  for file in (ROOT / "orchestrator/internal/handlers").glob("*.go"):
   source = file.read_text()
   for forbidden in ("QueueDeclare(", ".Consume(", ".ConsumeWithContext(", ".Publish("):
    self.assertNotIn(forbidden, source, f"{file}: {forbidden}")


if __name__ == '__main__': unittest.main()

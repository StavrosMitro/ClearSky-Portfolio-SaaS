"""docs/openapi.yaml lists exactly the gateway's routes (roadmap 2.8)."""
from pathlib import Path
import re
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
ROUTES = (ROOT / "orchestrator/internal/routes/routes.go").read_text()
SPEC = yaml.safe_load((ROOT / "docs/openapi.yaml").read_text())
METHODS = {"get", "post", "put", "patch", "delete"}


def gateway_routes():
    groups = {"r": ""}
    for var, parent, prefix in re.findall(r'(\w+)\s*:=\s*(\w+)\.Group\("([^"]*)"', ROUTES):
        groups[var] = (groups[parent].rstrip("/") + prefix).rstrip("/")
    routes = set()
    for var, method, path in re.findall(r'\b(\w+)\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"', ROUTES):
        full = groups[var] + ("" if path.startswith("/") else "/") + path
        routes.add((method.lower(), re.sub(r":(\w+)", r"{\1}", full)))
    return routes


def spec_routes():
    return {(m, path) for path, item in SPEC["paths"].items() for m in item if m in METHODS}


class OpenAPITests(unittest.TestCase):
    def test_spec_and_gateway_list_the_same_endpoints(self):
        gateway, spec = gateway_routes(), spec_routes()
        self.assertGreater(len(gateway), 25)
        self.assertEqual(sorted(gateway - spec), [], "routes missing from docs/openapi.yaml")
        self.assertEqual(sorted(spec - gateway), [], "documented routes the gateway does not serve")

    def test_error_codes_match_the_gateway(self):
        codes = set(re.findall(r'ErrorCode = "([A-Z_]+)"', "\n".join(
            p.read_text() for p in (ROOT / "orchestrator/internal/api").glob("*.go"))))
        documented = set(SPEC["components"]["schemas"]["ErrorEnvelope"]["properties"]["error"]["properties"]["code"]["enum"])
        self.assertEqual(documented, codes)

    def test_references_resolve(self):
        text = (ROOT / "docs/openapi.yaml").read_text()
        for kind, name in re.findall(r'"#/components/(\w+)/(\w+)"', text):
            self.assertIn(name, SPEC["components"][kind], f"{kind}/{name}")


if __name__ == "__main__":
    unittest.main()

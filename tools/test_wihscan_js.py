import unittest
from pathlib import Path

from tools.wihscan_js import (
    build_command,
    endpoint_from_item,
    is_excluded,
    load_rules,
    scan_content,
)


class WihscanAdapterTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        rules_path = Path(__file__).resolve().parent.parent / "wihscan" / "config" / "rules.yaml"
        if not rules_path.is_file():
            rules_path = Path(__file__).resolve().parents[2] / "WIHscan-1.0" / "config" / "rules.yaml"
        cls.rules, cls.excludes = load_rules(rules_path)

    def test_js_content_matches_without_leaking_values(self):
        content = 'const token = "eyJ' + "A" * 18 + "." + "B" * 20 + "." + "C" * 18 + '";\nconst password = "fakepass123";'
        findings = list(scan_content(content, "https://example.test/app.js", "javascript", self.rules, self.excludes))
        ids = {item["rule_id"] for item in findings}
        self.assertIn("jwt_token", ids)
        self.assertIn("password", ids)
        serialized = str(findings)
        self.assertNotIn("fakepass123", serialized)

    def test_exclusion_rule_is_respected(self):
        excludes = [{"id": "secret_key", "target": "regex:cc\\.163\\.com", "enabled": True}]
        self.assertTrue(is_excluded(excludes, "https://cc.163.com/app.js", "secret_key='fake-value'", "secret_key", "javascript"))
        self.assertFalse(is_excluded(excludes, "https://example.test/app.js", "secret_key='fake-value'", "secret_key", "javascript"))

    def test_generic_rules_ignore_permission_literals(self):
        content = "const permissions = { saveConfig: 'config:write', deleteUser: 'user:delete' };"
        findings = list(scan_content(content, "https://example.test/app.js", "javascript", self.rules, self.excludes))
        self.assertEqual(findings, [])

    def test_nested_request_and_xhr_fields_are_normalized(self):
        item = {
            "request": {"endpoint": "https://example.test/app.js", "tag": "script", "source": "https://example.test/"},
            "xhrrequests": [{"url": "https://example.test/api/users", "method": "post"}],
        }
        self.assertEqual(endpoint_from_item(item), ("https://example.test/app.js", "script", "https://example.test/"))

    def test_command_has_js_flags_and_no_full_port_scan(self):
        command = build_command(Path("katana.exe"), "https://example.test", 2, "30s", 10, 50, True)
        self.assertIn("-jc", command)
        self.assertIn("-jsl", command)
        self.assertIn("-xhr", command)
        self.assertNotIn("-p", command)
        self.assertNotIn("1-65535", command)


if __name__ == "__main__":
    unittest.main()

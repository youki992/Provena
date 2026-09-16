import unittest

from tools.arl_recon import header_rule_matches


class HeaderFingerprintTests(unittest.TestCase):
    def test_python_server_is_detected_without_simplehttp_false_positive(self):
        headers = {"Server": "SimpleHTTP/0.6 Python/3.13.5"}
        self.assertTrue(header_rule_matches("python", headers))
        self.assertFalse(header_rule_matches("eHTTP", headers))

    def test_header_name_and_delimited_value_are_supported(self):
        headers = {"testBanCookie": "enabled", "Server": "nginx/1.25"}
        self.assertTrue(header_rule_matches("testBanCookie", headers))
        self.assertTrue(header_rule_matches("nginx", headers))


if __name__ == "__main__":
    unittest.main()

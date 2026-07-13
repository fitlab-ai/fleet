import importlib.machinery
import importlib.util
import io
import json
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest import mock


def load_fleet():
    loader = importlib.machinery.SourceFileLoader("fleet_module", str(Path(__file__).parents[1] / "bin/fleet"))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


fleet = load_fleet()


def vmess(name="v"):
    return {"name": name, "type": "vmess", "server": "example.com", "port": 443,
            "uuid": "00000000-0000-0000-0000-000000000001"}


class NodeValidationTests(unittest.TestCase):
    def test_supported_protocols_and_counts(self):
        nodes = [vmess(), {"name": "h", "type": "hysteria2", "server": "h.example", "port": 443, "password": "p"},
                 {"name": "a", "type": "anytls", "server": "a.example", "port": 8443, "password": "p"}]
        validated, counts = fleet.validate_nodes(nodes)
        self.assertEqual(validated, nodes)
        self.assertEqual(counts, {"vmess": 1, "hysteria2": 1, "anytls": 1})

    def test_rejects_unknown_protocol_and_duplicate_names(self):
        with self.assertRaises(fleet.SubscriptionError):
            fleet.validate_nodes([dict(vmess(), type="trojan")])
        with self.assertRaises(fleet.SubscriptionError):
            fleet.validate_nodes([vmess(), vmess()])

    def test_quantity_guard(self):
        fleet.enforce_node_count(5, 10, False)
        with self.assertRaises(fleet.SubscriptionError):
            fleet.enforce_node_count(4, 10, False)
        fleet.enforce_node_count(1, 10, True)


class GenerationStoreTests(unittest.TestCase):
    def test_publish_and_load(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = fleet.GenerationStore(Path(tmp))
            store.publish(b"proxies: []\n", [vmess()], {"vmess": 1, "hysteria2": 0, "anytls": 0})
            self.assertEqual(store.load_nodes(), [vmess()])
            self.assertEqual((Path(tmp) / "current.json").stat().st_mode & 0o777, 0o600)

    def test_corrupt_current_generation_falls_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = fleet.GenerationStore(Path(tmp))
            store.publish(b"one", [vmess("old")], {"vmess": 1, "hysteria2": 0, "anytls": 0})
            store.publish(b"two", [vmess("new")], {"vmess": 1, "hysteria2": 0, "anytls": 0})
            current = json.loads((Path(tmp) / "current.json").read_text())["generation"]
            (Path(tmp) / "generations" / current / "nodes.json").write_text("broken")
            self.assertEqual(store.load_nodes()[0]["name"], "old")

    def test_bad_pointer_falls_back_to_legacy(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "current.json").write_text("not-json")
            (root / "nodes.json").write_text(json.dumps({"nodes": [vmess("legacy")]}))
            self.assertEqual(fleet.GenerationStore(root).load_nodes()[0]["name"], "legacy")

    def test_active_refresh_lock_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            root.joinpath("refresh.lock").write_text(str(__import__("os").getpid()))
            with self.assertRaises(fleet.SubscriptionError):
                with fleet.RefreshLock(root):
                    self.fail("lock should not be acquired")


class RefreshPipelineTests(unittest.TestCase):
    def test_process_validates_before_publishing(self):
        store = mock.Mock()
        store.load_nodes.return_value = [vmess("old")] * 10
        bad_yaml = b"proxies:\n  - name: bad\n    type: trojan\n    server: example.com\n    port: 443\n"
        with mock.patch.object(fleet, "_parse_subscription_yaml_strict", return_value={"proxies": [{"name": "bad", "type": "trojan", "server": "x", "port": 443}]}):
            with self.assertRaises(fleet.SubscriptionError):
                fleet._process_subscription(bad_yaml, store, False, check_sing_box=False)
        store.publish.assert_not_called()

    def test_force_does_not_bypass_protocol_validation(self):
        store = mock.Mock()
        store.load_nodes.return_value = [vmess("old")] * 10
        invalid = {"name": "bad", "type": "trojan", "server": "x", "port": 443}
        with mock.patch.object(fleet, "_parse_subscription_yaml_strict", return_value={"proxies": [invalid]}):
            with self.assertRaises(fleet.SubscriptionError):
                fleet._process_subscription(b"ignored", store, True, check_sing_box=False)
        store.publish.assert_not_called()

    def test_strict_parser_rejects_invalid_yaml(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "bad.yaml"
            path.write_text("proxies: [unterminated")
            with self.assertRaises(fleet.SubscriptionError):
                fleet._parse_subscription_yaml_strict(path)

    def test_sing_box_failure_identifies_node(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = Path(tmp) / "sing-box"
            binary.touch()
            failed = mock.Mock(returncode=1)
            with mock.patch.object(fleet.subprocess, "run", return_value=failed):
                with self.assertRaisesRegex(fleet.SubscriptionError, "broken-node"):
                    fleet.validate_with_sing_box([vmess("broken-node")], str(binary))


class SecureFileTests(unittest.TestCase):
    def test_secure_write_closes_fd_when_fdopen_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "secret"
            path.touch()
            with mock.patch.object(fleet.os, "open", return_value=123), \
                    mock.patch.object(fleet.os, "fdopen", side_effect=MemoryError), \
                    mock.patch.object(fleet.os, "close") as close:
                with self.assertRaises(MemoryError):
                    fleet._secure_write(path, b"secret")
            close.assert_called_once_with(123)


class CliSecurityTests(unittest.TestCase):
    def test_url_validation_requires_https(self):
        self.assertEqual(fleet.validate_subscription_url("https://example.com/a?token=x"), "https://example.com/a?token=x")
        with self.assertRaises(fleet.SubscriptionError):
            fleet.validate_subscription_url("http://example.com/token")

    def test_status_does_not_reveal_credential(self):
        backend = mock.Mock()
        backend.is_configured.return_value = True
        out = io.StringIO()
        with redirect_stdout(out):
            fleet.cmd_subscription_status(backend=backend, config_dir=Path("/nonexistent"))
        self.assertIn("Configured: yes", out.getvalue())
        self.assertNotIn("https://", out.getvalue())


if __name__ == "__main__":
    unittest.main()

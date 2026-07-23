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


def trojan(name="t", **overrides):
    node = {"name": name, "type": "trojan", "server": "trojan.example.com",
            "port": 443, "password": "trojan-password"}
    node.update(overrides)
    return node


def hysteria2(name="h", **overrides):
    node = {"name": name, "type": "hysteria2", "server": "h.example.com",
            "port": 443, "password": "hysteria-password"}
    node.update(overrides)
    return node


class MemoryCredentials(fleet.CredentialBackend):
    def __init__(self):
        self.urls = {}

    def set_url(self, subscription_id, url):
        self.urls[subscription_id] = url

    def get_url(self, subscription_id=None):
        if subscription_id not in self.urls:
            raise fleet.SubscriptionError("credential", "Subscription is not configured")
        return self.urls[subscription_id]

    def delete_url(self, subscription_id=None):
        self.urls.pop(subscription_id, None)


class NodeValidationTests(unittest.TestCase):
    def test_supported_protocols_and_counts(self):
        nodes = [vmess(), {"name": "h", "type": "hysteria2", "server": "h.example", "port": 443, "password": "p"},
                 {"name": "a", "type": "anytls", "server": "a.example", "port": 8443, "password": "p"},
                 trojan()]
        validated, counts = fleet.validate_nodes(nodes)
        self.assertEqual(validated, nodes)
        self.assertEqual(counts, {"vmess": 1, "hysteria2": 1, "anytls": 1,
                                  "trojan": 1})

    def test_rejects_unknown_protocol_and_duplicate_names(self):
        with self.assertRaises(fleet.SubscriptionError):
            fleet.validate_nodes([dict(vmess(), type="shadowsocks")])
        with self.assertRaises(fleet.SubscriptionError):
            fleet.validate_nodes([vmess(), vmess()])

    def test_trojan_rejects_invalid_tls_and_transport_fields(self):
        invalid_nodes = [
            trojan(password=""),
            trojan(sni=""),
            trojan(tls=False),
            trojan(tls="true"),
            trojan(alpn="h2"),
            trojan(alpn=["h2", ""]),
            trojan(alpn=[" "]),
            trojan(network="ws"),
            trojan(**{"ws-opts": {"path": "/"}}),
            trojan(**{"grpc-opts": {"grpc-service-name": "service"}}),
            trojan(**{"reality-opts": {"public-key": "key"}}),
            trojan(**{"client-fingerprint": "chrome"}),
        ]
        for node in invalid_nodes:
            with self.subTest(node=node):
                with self.assertRaises(fleet.SubscriptionError) as raised:
                    fleet.validate_nodes([node])
                self.assertEqual(raised.exception.category, "node")
                self.assertNotIn("trojan-password", str(raised.exception))

    def test_trojan_accepts_tcp_tls_fields(self):
        node = trojan(sni="sni.example.com", tls=True,
                      alpn=["h2", "http/1.1"], network="tcp",
                      **{"skip-cert-verify": True})
        validated, counts = fleet.validate_nodes([node])
        self.assertEqual(validated, [node])
        self.assertEqual(counts["trojan"], 1)

    def test_quantity_guard(self):
        fleet.enforce_node_count(5, 10, False)
        with self.assertRaises(fleet.SubscriptionError):
            fleet.enforce_node_count(4, 10, False)
        fleet.enforce_node_count(1, 10, True)

    def test_hysteria2_normalizes_connection_fields(self):
        node = hysteria2(
            ports="443,1000-1002", **{"hop-interval": 5,
            "obfs": "salamander", "obfs-password": "obfs-secret",
            "alpn": ["h3"]})
        validated, _ = fleet.validate_nodes([node])
        normalized = validated[0]["_fleet_hysteria2"]
        self.assertEqual(normalized["server_ports"], ["443:443", "1000:1002"])
        self.assertEqual(normalized["hop_interval"], "5s")
        self.assertEqual(normalized["obfs"], {
            "type": "salamander", "password": "obfs-secret",
        })

    def test_hysteria2_rejects_unsupported_and_invalid_fields_without_secrets(self):
        cases = [
            hysteria2(mport="443,8443"),
            hysteria2(obfs="salamander"),
            hysteria2(**{"obfs-password": "TOP-SECRET"}),
            hysteria2(ports="0,443"),
            hysteria2(**{"hop-interval": "10-5"}),
            hysteria2(**{"hop-interval": "5-10"}),
            hysteria2(obfs="gecko", **{"obfs-password": "TOP-SECRET"}),
            hysteria2(**{"obfs-min-packet-size": 512}),
            hysteria2(alpn=["h3", ""]),
            hysteria2(**{"bbr-profile": "standard"}),
            hysteria2(**{"bbr-profile": "unknown"}),
        ]
        for node in cases:
            with self.subTest(node=node):
                with self.assertRaises(fleet.SubscriptionError) as raised:
                    fleet.validate_nodes([node])
                self.assertEqual(raised.exception.category, "node")
                self.assertNotIn("TOP-SECRET", str(raised.exception))


class GenerationStoreTests(unittest.TestCase):
    def test_publish_and_load(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = fleet.GenerationStore(Path(tmp))
            store.publish(b"proxies: []\n", [vmess()], fleet._protocol_counts([vmess()]))
            self.assertEqual(store.load_nodes(), [vmess()])
            self.assertEqual((Path(tmp) / "current.json").stat().st_mode & 0o777, 0o600)

    def test_loads_legacy_manifest_without_trojan_count(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            store = fleet.GenerationStore(root)
            generation = store.publish(
                b"proxies: []\n", [vmess()], fleet._protocol_counts([vmess()]))
            manifest_path = root / "generations" / generation / "manifest.json"
            manifest = json.loads(manifest_path.read_text())
            manifest["protocol_counts"].pop("trojan", None)
            manifest_path.write_text(json.dumps(manifest))
            self.assertEqual(store.load_nodes(), [vmess()])

    def test_rejects_manifest_with_unknown_nonzero_protocol(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            store = fleet.GenerationStore(root)
            generation = store.publish(
                b"proxies: []\n", [vmess()], fleet._protocol_counts([vmess()]))
            manifest_path = root / "generations" / generation / "manifest.json"
            manifest = json.loads(manifest_path.read_text())
            manifest["protocol_counts"]["future-protocol"] = 1
            manifest_path.write_text(json.dumps(manifest))
            self.assertEqual(store.load_nodes(), [])

    def test_corrupt_current_generation_falls_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            store = fleet.GenerationStore(Path(tmp))
            store.publish(b"one", [vmess("old")], fleet._protocol_counts([vmess("old")]))
            store.publish(b"two", [vmess("new")], fleet._protocol_counts([vmess("new")]))
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

    def test_composite_writer_lock_releases_legacy_when_writer_is_busy(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            root.joinpath("writer.lock").write_text(str(__import__("os").getpid()))
            with self.assertRaises(fleet.SubscriptionError):
                with fleet.CompositeWriterLock(root):
                    self.fail("lock should not be acquired")
            self.assertFalse(root.joinpath("refresh.lock").exists())


class SubscriptionRegistryTests(unittest.TestCase):
    def test_names_are_case_insensitive_unique_and_removed_names_stay_reserved(self):
        with tempfile.TemporaryDirectory() as tmp:
            registry = fleet.SubscriptionRegistry(Path(tmp))
            first = registry.add("Airport")
            registry.save()
            with self.assertRaises(fleet.SubscriptionError):
                registry.add("airport")
            registry.mark_removed(first["id"])
            registry.save()
            self.assertEqual(registry.allocate_name(), "subscription-1")
            registry.add("subscription-1")
            self.assertEqual(registry.allocate_name(), "subscription-2")

    def test_registry_rejects_secret_fields_and_does_not_overwrite_corruption(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            path = root / "subscriptions.json"
            path.write_text('{"schema":2,"revision":1,"subscriptions":[{"id":"bad","name":"x","status":"active","url":"https://secret"}]}')
            with self.assertRaises(fleet.SubscriptionError):
                fleet.SubscriptionRegistry(root)
            self.assertIn("https://secret", path.read_text())

    def test_removed_records_are_purged_and_names_can_be_reused(self):
        with tempfile.TemporaryDirectory() as tmp:
            registry = fleet.SubscriptionRegistry(Path(tmp))
            removed = registry.add("subscription-1")
            registry.mark_removed(removed["id"])
            registry.save()
            purged = registry.purge_removed()
            registry.save()
            self.assertEqual([item["id"] for item in purged], [removed["id"]])
            self.assertEqual(registry.allocate_name(), "subscription-1")


class MultiSubscriptionTests(unittest.TestCase):
    def _published(self, root, record, nodes):
        store = fleet.GenerationStore(root / "subscriptions" / record["id"])
        store.publish(b"proxies: []\n", nodes, fleet._protocol_counts(nodes))

    def test_add_keeps_multiple_credentials_and_prints_stable_identity(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            out = io.StringIO()
            with redirect_stdout(out):
                self.assertEqual(fleet.cmd_subscription_add(
                    "airport-a", "https://a.example/sub?token=SECRET-A", backend, root), 0)
                self.assertEqual(fleet.cmd_subscription_add(
                    "airport-b", "https://b.example/sub?token=SECRET-B", backend, root), 0)
            registry = fleet.SubscriptionRegistry(root)
            self.assertEqual([r["name"] for r in registry.records], ["airport-a", "airport-b"])
            self.assertEqual(len(backend.urls), 2)
            self.assertNotIn("SECRET", (root / "subscriptions.json").read_text())
            self.assertNotIn("SECRET", out.getvalue())
            self.assertIn("ID:", out.getvalue())

    def test_legacy_cache_without_credential_is_not_hidden_by_first_add(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "nodes.json").write_text(json.dumps({"nodes": [vmess("legacy")]}))
            backend = MemoryCredentials()

            self.assertEqual(fleet.cmd_subscription_add(
                "new", "https://new.example/sub", backend, root), 1)

            self.assertFalse((root / "subscriptions.json").exists())
            self.assertEqual(fleet.GenerationStore(root).load_nodes(), [vmess("legacy")])

    def test_explicit_migrate_first_preserves_credentialed_legacy_subscription(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            backend.urls[None] = "https://legacy.example/sub"
            legacy_nodes = [vmess("legacy")]
            fleet.GenerationStore(root).publish(
                b"proxies: []\n", legacy_nodes, fleet._protocol_counts(legacy_nodes))

            source = root / "flclash.yaml"
            source.write_bytes(b"proxies: []\n")
            nodes = [vmess(f"v-{index}") for index in range(29)]
            nodes.extend({"name": f"h-{index}", "type": "hysteria2",
                          "server": "h.example", "port": 443, "password": "p"}
                         for index in range(4))
            nodes.extend({"name": f"a-{index}", "type": "anytls",
                          "server": "a.example", "port": 443, "password": "p"}
                         for index in range(11))

            with mock.patch.object(fleet, "_parse_subscription_yaml_strict",
                                   return_value={"proxies": nodes}), \
                    mock.patch.object(fleet, "validate_with_sing_box"):
                self.assertEqual(fleet.cmd_subscription_migrate(
                    source, "https://import.example/sub", backend, root), 0)

            registry = fleet.SubscriptionRegistry(root)
            self.assertEqual([record["name"] for record in registry.records],
                             ["subscription-1", "subscription-2"])
            self.assertEqual(fleet.load_aggregated_nodes(
                config_dir=root, warn=False)[0]["name"], "legacy")

    def test_migrate_reports_safe_subscription_error_detail(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / "flclash.yaml"
            source.write_bytes(b"proxies: []\n")
            out = io.StringIO()

            with mock.patch.object(fleet, "_parse_subscription_yaml_strict",
                                   return_value={"proxies": []}), redirect_stdout(out):
                self.assertEqual(fleet.cmd_subscription_migrate(
                    source, "http://invalid.example/sub", MemoryCredentials(), root), 1)

            self.assertIn("A valid HTTPS subscription URL is required", out.getvalue())

    def test_aggregation_preserves_order_source_and_duplicate_names(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            registry = fleet.SubscriptionRegistry(root)
            one = registry.add("one")
            two = registry.add("two")
            registry.save()
            self._published(root, one, [vmess("same"), vmess("one-only")])
            self._published(root, two, [vmess("same")])

            nodes = fleet.load_aggregated_nodes(config_dir=root, warn=False)

            self.assertEqual([node["name"] for node in nodes], ["same", "one-only", "same"])
            self.assertEqual([node["_fleet"]["subscription_name"] for node in nodes],
                             ["one", "one", "two"])
            self.assertEqual(len({node["_fleet"]["node_key"] for node in nodes}), 3)
            self.assertEqual(fleet._find_node(nodes, "@two/same"), nodes[2])
            out = io.StringIO()
            with redirect_stdout(out):
                self.assertIsNone(fleet._find_node(nodes, "same"))
            self.assertIn("@one/same", out.getvalue())
            self.assertIn("@two/same", out.getvalue())

    def test_remove_retains_marked_cache_until_refresh_purges_it(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            fleet.cmd_subscription_add("one", "https://one.example/sub", backend, root)
            record = fleet.SubscriptionRegistry(root).records[0]
            self._published(root, record, [vmess("cached")])

            self.assertEqual(fleet.cmd_subscription_remove("one", backend, root), 0)
            nodes = fleet.load_aggregated_nodes(config_dir=root, warn=False)
            self.assertEqual(nodes[0]["_fleet"]["subscription_status"], "removed")
            self.assertEqual(fleet.cmd_refresh(False, backend, root), 0)
            self.assertEqual(fleet.load_aggregated_nodes(config_dir=root, warn=False), [])
            self.assertFalse((root / "subscriptions" / record["id"]).exists())

    def test_refresh_isolates_failure_and_preserves_failed_cache(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            fleet.cmd_subscription_add("good", "https://good.example/sub", backend, root)
            fleet.cmd_subscription_add("bad", "https://bad.example/sub", backend, root)
            records = fleet.SubscriptionRegistry(root).records
            self._published(root, records[1], [vmess("last-good")])

            def download(url, **_kwargs):
                if "bad.example" in url:
                    raise fleet.SubscriptionError("download", "Download failed")
                return b"good"

            def process(_source, store, _force, check_sing_box=True):
                nodes = [vmess("fresh")]
                generation = store.publish(b"good", nodes, fleet._protocol_counts(nodes))
                return generation, nodes, fleet._protocol_counts(nodes)

            with mock.patch.object(fleet, "download_subscription", side_effect=download), \
                    mock.patch.object(fleet, "_process_subscription", side_effect=process), \
                    mock.patch.object(fleet, "_stop") as stop:
                self.assertEqual(fleet.cmd_refresh(False, backend, root), 1)
            stop.assert_not_called()
            nodes = fleet.load_aggregated_nodes(config_dir=root, warn=False)
            self.assertEqual([n["name"] for n in nodes], ["fresh", "last-good"])
            bad_state = json.loads((root / "subscriptions" / records[1]["id"] / "state.json").read_text())
            self.assertEqual(bad_state["last_error"], "download")

    def test_refresh_isolates_per_subscription_disk_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            fleet.cmd_subscription_add("broken", "https://broken.example/sub", backend, root)
            fleet.cmd_subscription_add("healthy", "https://healthy.example/sub", backend, root)
            calls = []

            def process(_source, store, _force, check_sing_box=True):
                calls.append(store.root.name)
                if len(calls) == 1:
                    raise OSError("disk unavailable")
                nodes = [vmess("healthy-node")]
                generation = store.publish(b"ok", nodes, fleet._protocol_counts(nodes))
                return generation, nodes, fleet._protocol_counts(nodes)

            with mock.patch.object(fleet, "download_subscription", return_value=b"source"), \
                    mock.patch.object(fleet, "_process_subscription", side_effect=process):
                self.assertEqual(fleet.cmd_refresh(False, backend, root), 1)

            self.assertEqual(len(calls), 2)
            self.assertEqual([n["name"] for n in fleet.load_aggregated_nodes(
                config_dir=root, warn=False)], ["healthy-node"])

    def test_refresh_publishes_trojan_count_to_subscription_state(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            fleet.cmd_subscription_add(
                "secondary", "https://secondary.example/sub", backend, root)
            record = fleet.SubscriptionRegistry(root).records[0]
            with mock.patch.object(fleet, "download_subscription", return_value=b"source"), \
                    mock.patch.object(fleet, "_parse_subscription_yaml_strict",
                                      return_value={"proxies": [trojan()]}), \
                    mock.patch.object(fleet, "validate_with_sing_box"):
                self.assertEqual(fleet.cmd_refresh(False, backend, root), 0)
            state = json.loads((root / "subscriptions" / record["id"] / "state.json").read_text())
            self.assertEqual(state["node_count"], 1)
            self.assertEqual(state["protocol_counts"], {
                "vmess": 0, "hysteria2": 0, "anytls": 0, "trojan": 1,
            })

    def test_remove_rolls_registry_back_when_credential_delete_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            backend = MemoryCredentials()
            fleet.cmd_subscription_add("one", "https://one.example/sub", backend, root)
            with mock.patch.object(backend, "delete_url",
                                   side_effect=fleet.SubscriptionError("credential", "failed")):
                self.assertEqual(fleet.cmd_subscription_remove("one", backend, root), 1)
            self.assertEqual(fleet.SubscriptionRegistry(root).records[0]["status"], "active")

    def test_ten_subscriptions_and_five_hundred_nodes_remain_distinct(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            registry = fleet.SubscriptionRegistry(root)
            for sub_index in range(10):
                record = registry.add(f"sub-{sub_index}")
                self._published(root, record, [vmess(f"node-{node_index}") for node_index in range(50)])
            registry.save()
            nodes = fleet.load_aggregated_nodes(config_dir=root, warn=False)
            self.assertEqual(len(nodes), 500)
            self.assertEqual(len({n["_fleet"]["node_key"] for n in nodes}), 500)


class KeychainCredentialTests(unittest.TestCase):
    def test_each_subscription_uses_a_distinct_keychain_account(self):
        results = [mock.Mock(returncode=0, stdout="") for _ in range(2)]
        runner = mock.Mock(side_effect=results)
        backend = fleet.KeychainCredentialBackend(runner=runner)
        one = "1" * 32
        two = "2" * 32
        with mock.patch.object(fleet.sys, "platform", "darwin"):
            backend.set_url(one, "https://one.example/sub")
            backend.set_url(two, "https://two.example/sub")
        accounts = [call.args[0][call.args[0].index("-a") + 1]
                    for call in runner.call_args_list]
        self.assertEqual(accounts, [f"{backend.username}:{one}", f"{backend.username}:{two}"])

class OutboundConfigTests(unittest.TestCase):
    def test_hysteria2_outbound_preserves_normalized_connection_semantics(self):
        validated, _ = fleet.validate_nodes([hysteria2(
            ports="443,1000-1002", **{"hop-interval": 5,
            "obfs": "salamander", "obfs-password": "secret",
            "alpn": ["h3"], "up": 100, "down": 200})])
        outbound = fleet._build_outbound(validated[0])
        self.assertNotIn("server_port", outbound)
        self.assertEqual(outbound["server_ports"], ["443:443", "1000:1002"])
        self.assertEqual(outbound["hop_interval"], "5s")
        self.assertEqual(outbound["obfs"], {"type": "salamander", "password": "secret"})
        self.assertEqual(outbound["tls"]["alpn"], ["h3"])
        self.assertEqual(fleet.mk_proxy_config(validated[0])["outbounds"][0],
                         fleet.mk_tun_config(validated[0])["outbounds"][0])

    def test_trojan_outbound_maps_tls_fields(self):
        node = trojan(sni="sni.example.com", alpn=["h2", "http/1.1"],
                      **{"skip-cert-verify": True})
        outbound = fleet._build_outbound(node)
        self.assertEqual(outbound, {
            "tag": "proxy", "type": "trojan", "server": "trojan.example.com",
            "server_port": 443, "password": "trojan-password",
            "tls": {"enabled": True, "server_name": "sni.example.com",
                    "insecure": True, "alpn": ["h2", "http/1.1"]},
        })

    def test_trojan_tls_defaults_and_proxy_tun_outbound_match(self):
        node = trojan(alpn=[])
        proxy_outbound = fleet.mk_proxy_config(node)["outbounds"][0]
        tun_outbound = fleet.mk_tun_config(node)["outbounds"][0]
        self.assertEqual(proxy_outbound, tun_outbound)
        self.assertEqual(proxy_outbound["tls"], {
            "enabled": True, "server_name": "trojan.example.com", "insecure": False,
        })


class RefreshPipelineTests(unittest.TestCase):
    def test_download_negotiates_clash_meta_without_changing_request_contract(self):
        class Response:
            status = 200

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def read(self, _limit):
                return b"proxies: []\n"

        opener = mock.Mock()
        opener.open.return_value = Response()
        url = "https://provider.example/subscription?token=test-token"

        self.assertEqual(fleet.download_subscription(url, opener=opener, timeout=17),
                         b"proxies: []\n")
        request = opener.open.call_args.args[0]
        self.assertEqual(request.full_url, url)
        self.assertEqual(request.get_header("User-agent"), "clash.meta")
        self.assertEqual(opener.open.call_args.kwargs["timeout"], 17)

    def test_strict_parser_distinguishes_format_from_clash_structure(self):
        cases = [
            ('"dm1lc3M6Ly9leGFtcGxl"\n', "format"),
            ('["vmess://example"]\n', "format"),
            ('{"rules": []}\n', "structure"),
        ]
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "source.yaml"
            path.write_text("ignored")
            for parsed, category in cases:
                with self.subTest(category=category, parsed=parsed):
                    result = mock.Mock(returncode=0, stdout=parsed)
                    with mock.patch.object(fleet.subprocess, "run", return_value=result):
                        with self.assertRaises(fleet.SubscriptionError) as raised:
                            fleet._parse_subscription_yaml_strict(path)
                    self.assertEqual(raised.exception.category, category)
                    self.assertNotIn("vmess://example", str(raised.exception))

    def test_format_failure_is_safe_and_preserves_current_generation(self):
        secrets = [
            "https://provider.example/subscription?token=TOP-SECRET-TOKEN",
            "TOP-SECRET-RESPONSE-vmess://credential",
            "TOP-SECRET-NODE",
        ]
        backend = mock.Mock()
        backend.get_url.return_value = secrets[0]

        for force in (False, True):
            with self.subTest(force=force), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / "current.json").write_text(json.dumps({"generation": "old-generation"}))
                out = io.StringIO()
                error = fleet.SubscriptionError(
                    "format",
                    "Subscription response is not supported Clash YAML; check format negotiation",
                )
                with mock.patch.object(fleet, "download_subscription",
                                       return_value=secrets[1].encode()), \
                        mock.patch.object(fleet, "_parse_subscription_yaml_strict",
                                          side_effect=error), \
                        redirect_stdout(out):
                    self.assertEqual(fleet.cmd_refresh(force, backend, root), 1)

                self.assertEqual(json.loads((root / "current.json").read_text()),
                                 {"generation": "old-generation"})
                record = fleet.SubscriptionRegistry(root).records[0]
                migrated_root = root / "subscriptions" / record["id"]
                self.assertEqual(json.loads((migrated_root / "current.json").read_text()),
                                 {"generation": "old-generation"})
                state_path = migrated_root / "state.json"
                state = json.loads(state_path.read_text())
                self.assertEqual(state["last_error"], "format")
                self.assertIn("check format negotiation", out.getvalue())
                for secret in secrets:
                    self.assertNotIn(secret, out.getvalue())
                    self.assertNotIn(secret, state_path.read_text())
                    self.assertNotIn(secret, (root / "subscriptions.json").read_text())

    def test_process_publishes_mixed_nodes_including_trojan(self):
        nodes = [vmess(f"v-{index}") for index in range(30)]
        nodes.extend({"name": f"h-{index}", "type": "hysteria2",
                      "server": "h.example", "port": 443, "password": "p"}
                     for index in range(4))
        nodes.append(trojan())
        store = mock.Mock()
        store.load_nodes.return_value = []
        store.publish.return_value = "new-generation"

        with mock.patch.object(fleet, "_parse_subscription_yaml_strict",
                               return_value={"proxies": nodes}):
            generation, published, counts = fleet._process_subscription(
                b"proxies: []\n", store, False, check_sing_box=False)

        self.assertEqual(generation, "new-generation")
        self.assertEqual(len(published), 35)
        self.assertEqual(counts, {"vmess": 30, "hysteria2": 4, "anytls": 0,
                                  "trojan": 1})
        store.publish.assert_called_once_with(b"proxies: []\n", nodes, counts)

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
        invalid = {"name": "bad", "type": "shadowsocks", "server": "x", "port": 443,
                   "password": "p"}
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
            version = mock.Mock(returncode=0, stdout="sing-box version 1.14.2\n")
            failed = mock.Mock(returncode=1)
            with mock.patch.object(fleet.subprocess, "run", side_effect=[version, failed]):
                with self.assertRaisesRegex(fleet.SubscriptionError, "broken-node"):
                    fleet.validate_with_sing_box([vmess("broken-node")], str(binary))


class DependencyVersionTests(unittest.TestCase):
    def test_accepts_current_stable_sing_box_version(self):
        result = mock.Mock(returncode=0, stdout="sing-box version 1.13.14\n")
        with mock.patch.object(fleet.subprocess, "run", return_value=result):
            self.assertEqual(fleet._require_sing_box_version("/tmp/sing-box"),
                             (1, 13, 14))

    def test_rejects_old_prerelease_or_unparseable_sing_box_versions(self):
        for result in (
                mock.Mock(returncode=0, stdout="sing-box version 1.13.13\n"),
                mock.Mock(returncode=0, stdout="sing-box version 1.14.0-alpha.50\n"),
                mock.Mock(returncode=0, stdout="not a version\n"),
                mock.Mock(returncode=1, stdout="")):
            with self.subTest(stdout=result.stdout), \
                    mock.patch.object(fleet.subprocess, "run", return_value=result):
                with self.assertRaisesRegex(fleet.SubscriptionError, "1.13.14"):
                    fleet._require_sing_box_version("/tmp/sing-box")


class DiagnosticsTests(unittest.TestCase):
    def test_ping_is_explicitly_tcp_only(self):
        out = io.StringIO()
        connection = mock.MagicMock()
        connection.__enter__.return_value = connection
        with mock.patch.object(fleet.socket, "create_connection", return_value=connection), \
                mock.patch.object(fleet, "_load_state", return_value={}), \
                redirect_stdout(out):
            self.assertEqual(fleet.cmd_ping([vmess()]), 0)
        text = out.getvalue()
        self.assertIn("TCP CONNECT", text)
        self.assertIn("REACHABLE", text)
        self.assertIn("does not verify the proxy protocol", text)
        self.assertNotIn("LATENCY", text)
        self.assertNotIn("TIMEOUT", text)

    def test_health_command_keeps_order_and_returns_nonzero(self):
        results = {
            "one": fleet.HealthResult("one", "HEALTHY", 12, "http"),
            "two": fleet.HealthResult("two", "UNHEALTHY", 20, "request_failed"),
        }
        nodes = [vmess("one"), vmess("two")]
        out = io.StringIO()
        with mock.patch.object(fleet, "_probe_proxy_health",
                               side_effect=lambda node: results[node["name"]]), \
                redirect_stdout(out):
            self.assertEqual(fleet.cmd_health(nodes), 1)
        text = out.getvalue()
        self.assertLess(text.index("one"), text.index("two"))
        self.assertIn("HEALTHY", text)
        self.assertIn("UNHEALTHY", text)

    def test_health_retries_after_early_core_exit(self):
        first = mock.Mock(pid=101)
        first.poll.return_value = 1
        second = mock.Mock(pid=202)
        second.poll.side_effect = [None, None]
        second.wait.return_value = 0
        connection = mock.MagicMock()
        connection.__enter__.return_value = connection
        curl = mock.Mock(returncode=0, stdout="204")
        with mock.patch.object(fleet.Path, "exists", return_value=True), \
                mock.patch.object(fleet, "_require_sing_box_version"), \
                mock.patch.object(fleet, "_allocate_local_port",
                                  side_effect=[41001, 41002]) as allocate, \
                mock.patch.object(fleet.subprocess, "Popen",
                                  side_effect=[first, second]) as popen, \
                mock.patch.object(fleet.socket, "create_connection",
                                  return_value=connection), \
                mock.patch.object(fleet.subprocess, "run", return_value=curl), \
                mock.patch.object(fleet.os, "killpg"):
            result = fleet._probe_proxy_health(vmess())
        self.assertEqual(result.status, "HEALTHY")
        self.assertEqual(allocate.call_count, 2)
        self.assertEqual(popen.call_count, 2)


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

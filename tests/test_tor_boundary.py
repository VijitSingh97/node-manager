"""Tor-only boundary tests: run with `python3 -m unittest tests.test_tor_boundary`. See docs/tor-boundary.md."""

import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

from tests import tor_boundary_lib as lib

ROOT = lib.ROOT


def read(path):
    return (ROOT / path).read_text()


def compose_files():
    return {p.name: p.read_text() for p in ROOT.glob("docker-compose*.yml")}


def mutated(text, old, new):
    assert old in text, old
    return text.replace(old, new, 1)


class ParserTest(unittest.TestCase):
    def test_subset_parser_reads_every_construct_the_compose_files_use(self):
        doc = lib.parse_yaml(
            "x-a: &a\n  k: v\nservices:\n  s:\n    logging: *a\n    test: [\"CMD\", \"x\"]\n"
            "    ports:\n      - target: 8000\n        host_ip: \"h\"\n      - \"1:2:3\"\n"
            "    entrypoint:\n      - /bin/sh\n      - |\n        echo hi\n        exit\n    flag: true\n"
        )
        s = doc["services"]["s"]
        self.assertEqual(s["logging"], {"k": "v"})
        self.assertEqual(s["test"], ["CMD", "x"])
        self.assertEqual(s["ports"], [{"target": 8000, "host_ip": "h"}, "1:2:3"])
        self.assertEqual(s["entrypoint"], ["/bin/sh", "echo hi\nexit\n"])
        self.assertIs(s["flag"], True)

    def test_parser_rejects_syntax_it_cannot_read(self):
        with self.assertRaises(ValueError):
            lib.parse_yaml("a: {b: c}\n")

    def test_merge_concatenates_lists_and_replaces_scalars(self):
        self.assertEqual(lib.merge({"a": [1], "b": 1, "c": {"d": 1}}, {"a": [2], "b": 2, "c": {"e": 2}}), {"a": [1, 2], "b": 2, "c": {"d": 1, "e": 2}})


class ComposeBoundaryTest(unittest.TestCase):
    def setUp(self):
        self.files = compose_files()

    def broken(self, name, old, new):
        files = dict(self.files)
        files[name] = mutated(files[name], old, new)
        return lib.compose_violations(files)

    def test_repository_compose_files_keep_every_service_but_tor_off_the_internet(self):
        self.assertEqual(lib.compose_violations(self.files), [])

    def test_the_checker_catches_each_way_to_open_a_route(self):
        base = "docker-compose.yml"
        cases = [
            ("    internal: true\n", "    internal: false\n", "btc_net is not internal"),
            ("  tor_egress:\n    driver: bridge\n", "  tor_egress:\n    driver: bridge\n  extra:\n    driver: bridge\n", "unexpected network set"),
            ("    dns:\n      - 127.0.0.1\n\nnetworks:", "    dns:\n      - 8.8.8.8\n\nnetworks:", "real DNS server"),
            ("    user: \"1000:1000\"\n    security_opt:\n      - no-new-privileges:true\n    cap_drop:\n      - ALL\n    # optional", "    user: \"1000:1000\"\n    network_mode: host\n    security_opt:\n      - no-new-privileges:true\n    cap_drop:\n      - ALL\n    # optional", "sets network_mode"),
            ("    cap_drop:\n      - ALL\n    # optional", "    cap_drop:\n      - ALL\n    extra_hosts:\n      - \"a:1.2.3.4\"\n    # optional", "sets extra_hosts"),
            ("      TOR_PROXY: 172.29.0.25:9050\n    volumes:\n      - ${MONERO", "      TOR_PROXY: 10.0.0.1:9050\n    volumes:\n      - ${MONERO", "TOR_PROXY is 10.0.0.1:9050"),
            ("      DBCACHE: ${BITCOIN_DBCACHE:-3000}\n", "      DBCACHE: ${BITCOIN_DBCACHE:-3000}\n      https_proxy: http://x\n", "proxy variable"),
            ("    volumes:\n      - ${BITCOIN_DATA_DIR:-./data/bitcoin}:/data\n", "    volumes:\n      - /var/run/docker.sock:/s\n      - ${BITCOIN_DATA_DIR:-./data/bitcoin}:/data\n", "Docker socket"),
            ("    cap_add: [\"CHOWN\", \"DAC_READ_SEARCH\"", "    cap_add: [\"NET_ADMIN\", \"CHOWN\"", "network capability"),
            ("        host_ip: \"${DASHBOARD_BIND_IP:-127.0.0.1}\"", "        host_ip: \"${DASHBOARD_BIND_IP:-0.0.0.0}\"", "loopback-default"),
            ("        ipv4_address: 172.29.0.25\n", "        ipv4_address: 172.29.0.99\n", "tor is not at 172.29.0.25"),
        ]
        for old, new, expected in cases:
            with self.subTest(expected=expected):
                files = dict(self.files)
                files[base] = mutated(files[base], old, new)
                found = lib.compose_violations(files)
                self.assertTrue(any(expected in item for item in found), found)

    def test_a_service_added_to_the_egress_network_is_flagged(self):
        files = dict(self.files)
        files["docker-compose.yml"] = mutated(files["docker-compose.yml"], "      btc_net:\n        ipv4_address: 172.29.0.27\n    dns:", "      btc_net:\n        ipv4_address: 172.29.0.27\n      tor_egress:\n        gw_priority: 1\n    dns:")
        self.assertTrue(any("dashboard networks" in item for item in lib.compose_violations(files)))

    def test_a_port_published_by_a_node_service_is_flagged(self):
        files = dict(self.files)
        files["docker-compose.utxo-index.yml"] = mutated(files["docker-compose.utxo-index.yml"], "    mem_limit: 1g\n", "    mem_limit: 1g\n    ports:\n      - \"8080:8000\"\n")
        found = lib.compose_violations(files)
        self.assertTrue(any("dashboard publishes a port" in item for item in found), found)

    def test_a_lan_port_must_require_a_bind_address(self):
        found = self.broken("docker-compose.rpc.yml", "BITCOIN_RPC_BIND_IP:?set bitcoin.rpc_bind_ip in config.json", "BITCOIN_RPC_BIND_IP:-0.0.0.0")
        self.assertTrue(any("required-bind-IP" in item for item in found), found)

    def test_an_unclassified_compose_file_is_flagged(self):
        files = dict(self.files)
        files["docker-compose.new.yml"] = "services: {}\n".replace("{}", "\n  x:\n    image: y\n")
        self.assertTrue(any("unclassified" in item for item in lib.compose_violations(files)))

    def test_every_compose_file_in_the_repository_is_classified(self):
        self.assertEqual(set(self.files), {"docker-compose.yml"} | set(lib.PORT_OVERLAYS) | lib.OTHER_OVERLAYS)


class TorContainerTest(unittest.TestCase):
    def test_relays_end_inside_the_stack_or_in_tors_socks_port(self):
        self.assertEqual(lib.tor_relay_violations(read("build/tor/entrypoint.sh"), compose_files()), [])

    def test_relay_checker_flags_a_clearnet_forward_and_a_new_listener(self):
        entry = read("build/tor/entrypoint.sh")
        flagged = lib.tor_relay_violations(mutated(entry, "TCP4:172.29.0.27:8000", "TCP4:93.184.216.34:8000"), compose_files())
        self.assertTrue(any("outside Tor" in item for item in flagged), flagged)
        flagged = lib.tor_relay_violations(entry + "socat TCP4-LISTEN:9999,fork TCP4:172.29.0.26:1 &\n", compose_files())
        self.assertTrue(any("do not match" in item for item in flagged), flagged)

    def test_tor_runs_as_a_client_with_no_control_or_relay_ports(self):
        self.assertEqual(lib.torrc_violations(read("build/tor/torrc")), [])
        for line in ("ORPort 9001", "ControlPort 9051", "ExitRelay 1", "TransPort 9040", "SocksPolicy accept *"):
            with self.subTest(line=line):
                self.assertTrue(lib.torrc_violations(read("build/tor/torrc") + line + "\n"))
        self.assertNotIn("ControlPort", read("build/tor/entrypoint.sh"))

    def test_the_dashboard_dials_the_tor_container_address(self):
        monitor = read("build/dashboard/monitor.go")
        self.assertIn(f'torProxy             = "{lib.TOR_PROXY}"', monitor)


class NodeConfigTest(unittest.TestCase):
    def test_bitcoin_core_routes_everything_through_tor(self):
        self.assertEqual(lib.bitcoin_violations(compose_files(), read("build/bitcoin/bitcoin.conf")), [])

    def test_bitcoin_checker_catches_direct_networking(self):
        files = compose_files()
        conf = read("build/bitcoin/bitcoin.conf")
        broken = dict(files)
        broken["docker-compose.yml"] = mutated(files["docker-compose.yml"], "-proxy=172.29.0.25:9050 -onion=172.29.0.25:9050", "-onion=172.29.0.25:9050")
        self.assertTrue(lib.bitcoin_violations(broken, conf))
        self.assertTrue(lib.bitcoin_violations(files, conf + "upnp=1\n"))
        self.assertTrue(lib.bitcoin_violations(files, conf + "connect=1.2.3.4\n"))
        self.assertTrue(lib.bitcoin_violations(files, mutated(conf, "listen=0", "listen=1")))

    def test_family_entrypoint_source_pins_tor_routing(self):
        script = read("build/bitcoin-family/entrypoint.sh")
        self.assertEqual(lib.family_entry_violations(script), [])
        self.assertTrue(lib.family_entry_violations(mutated(script, "proxy=$TOR_PROXY\nonion", "onion")))
        self.assertTrue(lib.family_entry_violations(mutated(script, "--socks5-hostname", "--proxy")))
        self.assertTrue(lib.family_entry_violations(mutated(script, "--noproxy ''", "--max-time 1")))

    def render_family(self, **env):
        with tempfile.TemporaryDirectory() as tmp:
            stub = Path(tmp) / "litecoind"
            stub.write_text('#!/bin/sh\ncat "${2#-conf=}"\n')
            stub.chmod(0o755)
            onion = Path(tmp) / "onion"
            onion.write_text("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz2345.onion\n")
            data = Path(tmp) / "data"
            data.mkdir()
            base = {"PATH": "{}:{}".format(tmp, os.environ["PATH"]), "NODE_BIN": "litecoin", "NODE_ENV_PREFIX": "LITECOIN", "TOR_PROXY": "t:1",
                    "LITECOIN_RPC_USER": "u", "LITECOIN_RPC_PASSWORD": "p", "LITECOIN_PRUNE": "1", "NODE_DATA_DIR": str(data),
                    "ONION_BIND": "172.29.0.33:9334", "ONION_HOSTNAME": str(onion)}
            base.update(env)
            result = subprocess.run(["sh", str(ROOT / "build/bitcoin-family/entrypoint.sh")], env=base, capture_output=True, text=True, check=True, cwd=tmp)
            return result.stdout

    def test_every_rendered_family_config_stays_on_tor(self):
        for clearnet in ("1", "0"):
            for inbound in ("1", "0"):
                with self.subTest(clearnet=clearnet, inbound=inbound):
                    conf = self.render_family(TOR_CLEARNET=clearnet, TOR_INBOUND=inbound)
                    self.assertEqual(lib.family_conf_violations(conf, clearnet == "1", inbound == "1", "t:1"), [])
                    self.assertEqual("listen=1" in conf.splitlines(), inbound == "1")

    def test_family_config_checker_flags_direct_peers_and_port_mapping(self):
        good = self.render_family(TOR_CLEARNET="1", TOR_INBOUND="1")
        for extra in ("upnp=1", "natpmp=1", "connect=1.2.3.4:8333", "externalip=1.2.3.4", "bind=0.0.0.0", "addnode=seed.example:1"):
            with self.subTest(extra=extra):
                self.assertTrue(lib.family_conf_violations(good + extra + "\n", True, True, "t:1"))
        self.assertTrue(lib.family_conf_violations(good.replace("proxy=t:1\n", ""), True, True, "t:1"))
        self.assertTrue(lib.family_conf_violations(good.replace("listen=1\n", "listen=0\n").replace("onion=", "xonion="), True, False, "t:1"))
        onion_only = self.render_family(TOR_CLEARNET="0", TOR_INBOUND="0")
        self.assertTrue(lib.family_conf_violations(onion_only.replace("onlynet=onion\n", ""), False, False, "t:1"))

    def test_monero_has_no_direct_peers_or_update_checks(self):
        entry, wallet = read("build/monero/entrypoint.sh"), read("build/monero/wallet-entrypoint.sh")
        self.assertEqual(lib.monero_violations(entry, wallet), [])
        for old, new in (("proxy=socks5://$TOR_PROXY", "proxy=none"), ("tx-proxy=tor,$TOR_PROXY,disable_noise", "pad-transactions=1"),
                         ("in-peers=0", "in-peers=8"), ("check-updates=disabled", "check-updates=notify"),
                         ("hide-my-port=1", "hide-my-port=0"), ("disable-dns-checkpoints=1", "x=1")):
            with self.subTest(old=old):
                self.assertTrue(lib.monero_violations(mutated(entry, old, new), wallet))
        self.assertTrue(lib.monero_violations(entry + "add-priority-node=1.2.3.4\n", wallet))
        self.assertTrue(lib.monero_violations(entry, wallet + "proxy=socks5://1.2.3.4:1\n"))

    def test_tari_node_and_wallet_dial_only_through_tor(self):
        entry, node, wallet = read("build/tari/entrypoint.sh"), read("build/tari/config.toml"), read("build/tari-wallet/config.toml")
        self.assertEqual(lib.tari_violations(entry, node, wallet), [])
        for text, old, new, which in (
            (node, "proxy_bypass_for_outbound_tcp = false", "proxy_bypass_for_outbound_tcp = true", "node"),
            (wallet, "proxy_bypass_addresses = []", 'proxy_bypass_addresses = ["1.2.3.4"]', "wallet"),
            (node, 'socks_address_override = "/ip4/172.29.0.25/tcp/9050"', 'socks_address_override = "/ip4/9.9.9.9/tcp/9050"', "node"),
            (node, "dns_seeds = []", 'dns_seeds = ["seeds.example"]', "node"),
            (wallet, 'type = "tor"', 'type = "tcp"', "wallet"),
            (wallet, "check_interval = 0", "check_interval = 12", "wallet"),
            (wallet, "use_libtor = false", "use_libtor = true", "wallet"),
        ):
            with self.subTest(old=old):
                if which == "node":
                    self.assertTrue(lib.tari_violations(entry, mutated(node, old, new), wallet))
                else:
                    self.assertTrue(lib.tari_violations(entry, node, mutated(wallet, old, new)))
        self.assertTrue(lib.tari_violations(mutated(entry, "transport=tcp_tor", "transport=tcp"), node, wallet))

    def test_zakura_peers_are_exactly_the_tor_socks_relays(self):
        relays = [int(p) for p in re.findall(r"TCP4-LISTEN:(1823[3-6])", read("build/tor/entrypoint.sh"))]
        self.assertEqual(len(relays), 4)
        for name in ("zakurad.toml", "zakurad-address-index.toml"):
            with self.subTest(name=name):
                toml = read("build/zcash/" + name)
                self.assertEqual(lib.zcash_violations(toml, relays), [])
                self.assertTrue(lib.zcash_violations(mutated(toml, '"172.29.0.25:18233"', '"1.2.3.4:8233"'), relays))
                self.assertTrue(lib.zcash_violations(mutated(toml, "cache_dir = false", "cache_dir = true"), relays))
                self.assertTrue(lib.zcash_violations(mutated(toml, "127.0.0.1:8233", "0.0.0.0:8233"), relays))


class HostScriptTest(unittest.TestCase):
    def test_no_unlisted_network_command_exists_in_scripts_entrypoints_or_compose(self):
        allowlist = lib.parse_allowlist(read("tests/tor_boundary_allowlist.txt"))
        self.assertEqual(lib.network_violations(list(lib.scan_paths()), allowlist), [])

    def test_scanner_flags_commands_that_could_leave_the_host(self):
        for script in (
            "curl https://example.com\n", "wget -q http://x\n", "if ! curl -s https://x; then :; fi\n", "x=$(curl http://127.0.0.1 https://y)\n",
            "docker run --rm alpine true\n", "git clone https://x\n", "docker compose pull\n", "compose up -d\n", "ssh host true\n",
            "nc -z 8.8.8.8 53\n", "RUN pip install x\n", "apt-get install -y y\n", "exec 3<>/dev/tcp/1.2.3.4/80\n", "import socket\n",
            "curl --socks5-hostname 1.2.3.4:9050 https://x\n", "sudo apt update\n", "FOO=1 curl https://x\n", "docker buildx build .\n",
            "dig example.com\n", "nslookup x\n", "host example.com\n", "/usr/bin/curl https://evil.example.org\n", "env curl https://x\n",
            "env FOO=1 curl http://x\n", "timeout 5 curl https://x\n", "nohup curl https://x &\n", "sudo -u root curl https://x\n",
            "bash -c \"curl https://x\"\n", "sh -c 'wget x'\n", "busybox wget x\n", "docker exec c curl https://x\n",
            "docker exec -it -u root c curl https://x\n", "\"$docker_bin\" exec c wget https://x\n", "docker compose exec tor wget x\n",
            "openssl s_client -connect x:443\n", "lynx x\n", "python3 -c \"import urllib.request as u;u.urlopen('http://x')\"\n",
            "python3 -c 'import socket'\n", "tailscale up\n", "nmap -sn 10.0.0.0/24\n",
        ):
            with self.subTest(script=script):
                self.assertTrue(lib.network_hits(script), script)

    def test_scanner_allows_loopback_tor_and_offline_forms(self):
        for script in (
            "curl -fsS http://127.0.0.1:8232/\n", 'curl -q --socks5-hostname "$TOR_PROXY" https://x\n', "docker run --rm --network none alpine\n",
            "compose up -d --pull never --no-build tor\n", "nc -z 127.0.0.1 9050\n", "# curl https://example.com\n", 'echo "use curl later"\n',
            "cat <<EOF\ncurl https://example.com\nEOF\n", "socat TCP4-LISTEN:1,fork TCP4:172.29.0.27:8000 &\n", "git rev-parse HEAD\n",
            "case x in brew) ;; 1 | apt) ;; esac\n", "brew services start node-manager\n",
            "command -v curl >/dev/null\n", "openssl rand -hex 8\n", "for host in $HOSTS; do echo $host; done\n", "docker exec c ls /data\n",
            "timeout 5 ls\n", "sudo systemctl restart x\n", 'bash -c "echo hi"\n', "python3 -c 'print(1)'\n", "exec \"$@\"\n",
        ):
            with self.subTest(script=script):
                self.assertEqual(lib.network_hits(script), [], script)

    def test_allowlist_must_be_exact_and_justified(self):
        sources = [("a.sh", "curl https://example.com\n")]
        self.assertTrue(lib.network_violations(sources, {}))
        self.assertTrue(lib.network_violations(sources, {("a.sh", "curl"): 2}))
        self.assertTrue(lib.network_violations([("a.sh", "true\n")], {("a.sh", "curl"): 1}))
        self.assertEqual(lib.network_violations(sources, {("a.sh", "curl"): 1}), [])
        for bad in ("a | curl | 1 | install-time |", "a | curl | 1 | because | why", "a | curl | x | install-time | why", "a | curl | 1 | dev-tool"):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                lib.parse_allowlist(bad)

    def test_every_scanned_host_file_exists(self):
        names = {path for path, _ in lib.scan_paths()}
        for expected in ("stack", "configure.sh", "scripts/node-control", "scripts/node-port-control", "scripts/node-resource-profile",
                         "packaging/postinst", "build/tor/entrypoint.sh", "build/monero/Dockerfile", "docker-compose.yml"):
            self.assertIn(expected, names)


class RuntimeProbeWiringTest(unittest.TestCase):
    def test_the_container_e2e_keeps_its_direct_egress_probes(self):
        e2e = read("tests/test_e2e.sh")
        for marker in (
            "docker compose ps --services --status running",
            "nc -z -w 10 1.1.1.1 443",
            "nc -z -w 3 1.1.1.1 443",
            "nc -z -w 3 2606:4700:4700::1111 443",
            "nslookup example.com'",
            "nslookup example.com 8.8.8.8",
            "clearnet egress from",
        ):
            with self.subTest(marker=marker):
                self.assertIn(marker, e2e)
        self.assertRegex(e2e, r'for probed in "\$\{probed_services\[@\]\}"; do')

    def test_the_boundary_tests_run_with_the_suite(self):
        runner = read("tests/run.sh")
        self.assertIn("tests.test_tor_boundary", runner)
        self.assertIn("tests/test_e2e.sh", runner)


if __name__ == "__main__":
    unittest.main()

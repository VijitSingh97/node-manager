"""Static checkers for the Tor-only boundary (docs/tor-boundary.md), stdlib only."""

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TOR_IP = "172.29.0.25"
TOR_PROXY = TOR_IP + ":9050"
STACK_SUBNET_PREFIX = "172.29.0."

KEY = re.compile(r"^([A-Za-z0-9_.\-]+):(?:\s+(.*))?$")


def parse_yaml(text):
    """Parse the block-style YAML subset the compose files use; fails loudly on anything else."""
    lines = text.splitlines()
    anchors = {}

    def indent(line):
        return len(line) - len(line.lstrip(" "))

    def skip(i):
        while i < len(lines) and (not lines[i].strip() or lines[i].lstrip().startswith("#")):
            i += 1
        return i

    def block_scalar(i, parent):
        body = []
        while i < len(lines) and (not lines[i].strip() or indent(lines[i]) > parent):
            body.append(lines[i])
            i += 1
        width = min((indent(x) for x in body if x.strip()), default=0)
        return "\n".join(x[width:] for x in body).rstrip("\n") + "\n", i

    def scalar(raw):
        raw = raw.strip()
        if raw.startswith("*"):
            return anchors[raw[1:]]
        if raw.startswith("["):
            return json.loads(raw)
        if raw.startswith('"'):
            return json.loads(raw)
        if raw.startswith("'"):
            return raw[1:-1].replace("''", "'")
        if raw.startswith(("{", "&")):
            raise ValueError("unsupported YAML: " + raw)
        raw = re.sub(r"\s+#.*$", "", raw)
        if raw in ("true", "false"):
            return raw == "true"
        return int(raw) if re.fullmatch(r"\d+", raw) else raw

    def node(i):
        i = skip(i)
        if lines[i].lstrip().startswith("- ") or lines[i].strip() == "-":
            return parse_list(i, indent(lines[i]))
        return parse_map(i, indent(lines[i]))

    def parse_list(i, ind):
        items = []
        while True:
            i = skip(i)
            if i >= len(lines) or indent(lines[i]) != ind or not lines[i].lstrip().startswith("-"):
                return items, i
            rest = lines[i].lstrip()[1:].strip()
            if rest in ("|", ">"):
                value, i = block_scalar(i + 1, ind)
                items.append(value)
            elif KEY.match(rest) and not rest.startswith(('"', "'")):
                lines[i] = " " * (ind + 2) + rest
                value, i = parse_map(i, ind + 2)
                items.append(value)
            else:
                items.append(scalar(rest))
                i += 1

    def parse_map(i, ind):
        result = {}
        while True:
            i = skip(i)
            if i >= len(lines) or indent(lines[i]) != ind or lines[i].lstrip().startswith("- "):
                return result, i
            match = KEY.match(lines[i].strip())
            if not match:
                raise ValueError("unsupported YAML line: " + lines[i])
            key, rest = match.group(1), (match.group(2) or "").strip()
            anchor = None
            if rest.startswith("&"):
                anchor, _, rest = rest[1:].partition(" ")
                rest = rest.strip()
            if rest in ("|", ">"):
                value, i = block_scalar(i + 1, ind)
            elif rest:
                value, i = scalar(rest), i + 1
            else:
                j = skip(i + 1)
                nested = j < len(lines) and (
                    indent(lines[j]) > ind or (indent(lines[j]) == ind and lines[j].lstrip().startswith("- "))
                )
                value, i = node(j) if nested else (None, i + 1)
            if anchor:
                anchors[anchor] = value
            result[key] = value

    value, _ = node(0)
    return value


def merge(base, overlay):
    """Merge like Compose: maps recurse, lists concatenate, scalars are replaced."""
    if isinstance(base, dict) and isinstance(overlay, dict):
        return {k: merge(base[k], overlay[k]) if k in base and k in overlay else base[k] if k in base else overlay[k] for k in {**base, **overlay}}
    if isinstance(base, list) and isinstance(overlay, list):
        return base + overlay
    return overlay


PORT_OVERLAYS = {
    "docker-compose.rpc.yml": "BITCOIN_RPC",
    "docker-compose.monero-rpc.yml": "MONERO_RPC",
    "docker-compose.monero-zmq.yml": "MONERO_ZMQ",
    "docker-compose.tari-grpc.yml": "TARI_GRPC",
    "docker-compose.litecoin-rpc.yml": "LITECOIN_RPC",
    "docker-compose.dogecoin-rpc.yml": "DOGECOIN_RPC",
    "docker-compose.bitcoincash-rpc.yml": "BITCOINCASH_RPC",
    "docker-compose.zcash-rpc.yml": "ZCASH_RPC",
}
OTHER_OVERLAYS = {
    "docker-compose.tari-wallet.yml",
    "docker-compose.utxo-index.yml",
    "docker-compose.litecoin-utxo-index.yml",
    "docker-compose.bitcoincash-utxo-index.yml",
    "docker-compose.dogecoin-utxo-index.yml",
}
BAD_CAPS = {"NET_ADMIN", "NET_RAW", "SYS_ADMIN", "ALL"}
PROXY_ENV = re.compile(r"^(http|https|ftp|all|no)_proxy$", re.IGNORECASE)
LAN_PORT = re.compile(r"^\$\{(?P<prefix>[A-Z_]+)_BIND_IP:\?[^}]+\}:\$\{(?P=prefix)_PORT:-(\d+)\}:(?P<target>\d+)$")


def load_compose(files):
    """Return (merged document, per-file documents) from a {name: text} mapping."""
    docs = {name: parse_yaml(text) for name, text in files.items()}
    merged = {}
    for name in sorted(docs, key=lambda n: (n != "docker-compose.yml", n)):
        merged = merge(merged, docs[name])
    return merged, docs


ZAKURA_ENV = {"CONFIG_FILE_PATH", "ZAKURA_STATE__CACHE_DIR", "ZAKURA_RPC__COOKIE_DIR"}


def compose_violations(files):
    merged, docs = load_compose(files)
    found = []
    expected = {"docker-compose.yml"} | set(PORT_OVERLAYS) | OTHER_OVERLAYS
    if set(files) != expected:
        found.append(f"unclassified compose files: {sorted(set(files) ^ expected)}")

    networks = merged.get("networks", {})
    if set(networks) != {"btc_net", "tor_egress"}:
        found.append(f"unexpected network set {sorted(networks)}")
    if (networks.get("btc_net") or {}).get("internal") is not True:
        found.append("btc_net is not internal")
    if (networks.get("tor_egress") or {}).get("internal"):
        found.append("tor_egress must be the one network with an external route")

    services = merged.get("services", {})
    if "tor" not in services:
        found.append("tor service missing")
    for name, service in services.items():
        attached = set((service.get("networks") or {}).keys()) if isinstance(service.get("networks"), dict) else set(service.get("networks") or [])
        want = {"btc_net", "tor_egress"} if name == "tor" else {"btc_net"}
        if attached != want:
            found.append(f"{name} networks {sorted(attached)}, expected {sorted(want)}")
        for key in ("network_mode", "extra_hosts", "privileged", "devices", "sysctls", "cgroup_parent", "links", "external_links"):
            if service.get(key):
                found.append(f"{name} sets {key}")
        for key in ("pid", "ipc", "userns_mode", "uts"):
            if service.get(key) == "host":
                found.append(f"{name} sets {key}: host")
        if name != "tor" and service.get("dns") != ["127.0.0.1"]:
            found.append("{} can use a real DNS server: {}".format(name, service.get("dns")))
        if name != "tor" and service.get("ports"):
            found.append(f"{name} publishes a port")
        if BAD_CAPS & set(service.get("cap_add") or []):
            found.append(f"{name} adds a network capability")
        for volume in service.get("volumes") or []:
            if "docker.sock" in str(volume):
                found.append(f"{name} mounts the Docker socket")
        env = service.get("environment") or {}
        for var, value in (env.items() if isinstance(env, dict) else []):
            if PROXY_ENV.match(var):
                found.append(f"{name} sets proxy variable {var}")
            if var == "TOR_PROXY" and value != TOR_PROXY:
                found.append(f"{name} TOR_PROXY is {value}")
        if name == "zcash" and set(env) - ZAKURA_ENV:
            found.append(f"zcash sets unpinned Zakura variables {sorted(set(env) - ZAKURA_ENV)}")
    tor_networks = ((services.get("tor") or {}).get("networks") or {})
    if (tor_networks.get("btc_net") or {}).get("ipv4_address") != TOR_IP:
        found.append(f"tor is not at {TOR_IP} on btc_net")

    base_ports = (docs["docker-compose.yml"]["services"]["tor"]).get("ports") or []
    ok = len(base_ports) == 1 and base_ports[0].get("target") == 8000 and base_ports[0].get("host_ip") == "${DASHBOARD_BIND_IP:-127.0.0.1}"
    if not ok:
        found.append("base compose must publish only the dashboard relay on a loopback-default address")
    for name, doc in docs.items():
        if name == "docker-compose.yml":
            continue
        for service_name, service in (doc.get("services") or {}).items():
            published = service.get("ports") or []
            if name in PORT_OVERLAYS:
                match = len(published) == 1 and isinstance(published[0], str) and LAN_PORT.match(published[0])
                if service_name != "tor" or not match or match.group("prefix") != PORT_OVERLAYS[name]:
                    found.append(f"{name} must publish one required-bind-IP port on tor")
            elif published:
                found.append(f"{name} publishes a port")
            if name not in PORT_OVERLAYS and service_name == "tor":
                found.append(f"{name} changes the tor service")
            if name not in PORT_OVERLAYS and service.get("networks") and service_name != "tari-wallet":
                found.append(f"{name} changes networks of {service_name}")
    for name in PORT_OVERLAYS:
        if name in docs and set(docs[name].get("services") or {}) != {"tor"}:
            found.append(f"{name} touches a service other than tor")
    return found


def tor_relay_violations(entrypoint, files):
    """Every relay in the Tor entrypoint must terminate inside the stack or in Tor's own SOCKS port."""
    found = []
    overlay_targets = set()
    for name in PORT_OVERLAYS:
        if name in files:
            overlay_targets.add(int(LAN_PORT.match(parse_yaml(files[name])["services"]["tor"]["ports"][0]).group("target")))
    listeners = {int(p) for p in re.findall(r"TCP4-LISTEN:(\d+)", entrypoint)}
    seeds = {18233, 18234, 18235, 18236}
    if listeners != {8000} | overlay_targets | seeds:
        found.append(f"Tor listeners {sorted(listeners)} do not match the published relays")
    for line in entrypoint.splitlines():
        if "TCP4-LISTEN" not in line:
            continue
        socks = re.search(r"SOCKS4A:127\.0\.0\.1:[a-z0-9.\-]+:\d+,socksport=9050\b", line)
        stack = re.search(r"\sTCP4:172\.29\.0\.\d+:\d+(\s|$)", line)
        if not (socks or stack):
            found.append("relay leaves the stack outside Tor: " + line.strip())
    for forbidden in ("TCP:", "TCP6", "UDP", "OPENSSL", "SOCKS5", "SOCKS4:", "EXEC", "SYSTEM", "PROXY"):
        if re.search(r"socat[^\n]*\s" + forbidden, entrypoint):
            found.append("unexpected socat address type " + forbidden)
    return found


def torrc_violations(text):
    found = []
    for directive in ("ORPort", "DirPort", "ExitRelay", "BridgeRelay", "ControlPort", "TransPort", "DNSPort", "SocksPolicy"):
        if re.search(r"^\s*" + directive + r"\b", text, re.MULTILINE | re.IGNORECASE):
            found.append("torrc sets " + directive)
    if not re.search(r"^SocksPort 0\.0\.0\.0:9050$", text, re.MULTILINE):
        found.append("torrc SocksPort changed")
    return found


def compose_script(files, service, key="entrypoint"):
    merged, _ = load_compose(files)
    value = merged["services"][service][key]
    return "\n".join(value) if isinstance(value, list) else value


def bitcoin_violations(files, conf):
    script = compose_script(files, "bitcoin")
    found = []
    if f'NET_ARGS="-proxy={TOR_PROXY} -onion={TOR_PROXY}"' not in script:
        found.append("bitcoind does not set both proxies to Tor")
    if 'NET_ARGS="$$NET_ARGS -onlynet=onion"' not in script:
        found.append("bitcoind lost the onion-only opt-out")
    if "-listenonion=0" not in script:
        found.append("bitcoind may open Tor control connections")
    if not re.search(r"^listen=0\s*(#.*)?$", conf, re.MULTILINE):
        found.append("bitcoin.conf listens by default")
    if not re.search(r"^natpmp=0\s*(#.*)?$", conf, re.MULTILINE):
        found.append("bitcoin.conf does not turn NAT-PMP off")
    if re.search(r"^\s*upnp\s*=", conf, re.MULTILINE) or "-upnp" in script:
        found.append("Bitcoin Core 32.1 has no -upnp option and refuses to start with one")
    for pattern in (r"^\s*(upnp|natpmp|discover)\s*=\s*1", r"^\s*(connect|seednode|externalip|proxy|onion)\s*="):
        if re.search(pattern, conf, re.MULTILINE):
            found.append("bitcoin.conf contains " + pattern)
    for flag in ("-upnp=1", "-natpmp=1", "-discover=1", "-dns=1", "-proxy=127", "-connect="):
        if flag in script:
            found.append("bitcoind started with " + flag)
    return found


def family_entry_violations(script):
    found = []
    for pattern, message in (
        (r"^\s*proxy=\$TOR_PROXY$", "no Tor proxy in the family config"),
        (r"^\s*onion=\$TOR_PROXY$", "no Tor onion proxy in the family config"),
        (r"^\s*discover=0$", "family nodes discover local addresses"),
        (r"^\s*upnp=0$", "family nodes do not turn UPnP off"),
        (r"\[ \"\$NODE_BIN\" != bitcoin \] \|\| echo natpmp=0$", "Bitcoin Cash does not turn NAT-PMP off"),
        (r"--socks5-hostname \"\$TOR_PROXY\"", "seed lookup does not use Tor with remote DNS"),
        (r"--proto =https", "seed lookup is not HTTPS-only"),
        (r"--noproxy ''", "seed lookup honours proxy environment variables"),
        (r"onlynet=onion\\ndnsseed=0", "onion-only mode still queries DNS seeds"),
    ):
        if not re.search(pattern, script, re.MULTILINE):
            found.append(message)
    for forbidden in (r"^\s*upnp=1", r"^\s*natpmp=1", r"^\s*connect=", r"^\s*seednode="):
        if re.search(forbidden, script, re.MULTILINE):
            found.append("family config contains " + forbidden)
    return found


def family_conf_violations(conf, clearnet, inbound, proxy="t:1", node_bin="litecoin"):
    """Check a conf rendered by build/bitcoin-family/entrypoint.sh."""
    lines = conf.splitlines()
    found = []
    for required in ("proxy=" + proxy, "onion=" + proxy, "discover=0", "upnp=0") + (("natpmp=0",) if node_bin == "bitcoin" else ()):
        if required not in lines:
            found.append("missing " + required)
    if not clearnet:
        for required in ("onlynet=onion", "dnsseed=0"):
            if required not in lines:
                found.append("onion-only config missing " + required)
    for line in lines:
        key, _, value = line.partition("=")
        if key in ("upnp", "natpmp") and value != "0":
            found.append("port mapping enabled: " + line)
        if key == "connect":
            found.append("direct connect: " + line)
        if key == "externalip" and not value.endswith(".onion"):
            found.append("clearnet externalip: " + line)
        if key == "bind" and not (value == "127.0.0.1" or value.endswith("=onion")):
            found.append("listener bound beyond loopback or the onion target: " + line)
        if key == "addnode" and not re.fullmatch(r"\d+\.\d+\.\d+\.\d+:\d+", value):
            found.append("addnode is not a numeric peer: " + line)
    if not inbound and "listen=0" not in lines:
        found.append("node listens without inbound Tor")
    return found


def monero_violations(entry, wallet):
    found = []
    for pattern, message in (
        (r"^proxy=socks5://\$TOR_PROXY$", "Monero P2P proxy is not Tor"),
        (r"^tx-proxy=tor,\$TOR_PROXY,disable_noise$", "Monero transactions bypass Tor"),
        (r"^in-peers=0$", "Monero accepts direct inbound peers"),
        (r"^hide-my-port=1$", "Monero advertises its port"),
        (r"^no-igd=1$", "Monero maps its port with UPnP"),
        (r"^disable-dns-checkpoints=1$", "Monero uses DNS checkpoints"),
        (r"^check-updates=disabled$", "Monero checks for updates"),
    ):
        if not re.search(pattern, entry, re.MULTILINE):
            found.append(message)
    if re.search(r"^(p2p-external-port|add-peer|add-priority-node|add-exclusive-node|seed-node|bootstrap-daemon-address)=", entry, re.MULTILINE):
        found.append("Monero is pinned to clearnet peers")
    if not re.search(r"^daemon-address=http://172\.29\.0\.30:18085$", wallet, re.MULTILINE):
        found.append("Monero wallet daemon is not the internal node")
    if re.search(r"^(proxy|tx-proxy|bootstrap-daemon-address)=", wallet, re.MULTILINE):
        found.append("Monero wallet has its own network settings")
    return found


def toml_value(text, section, key):
    body = re.search(r"^\[" + re.escape(section) + r"\]\n(.*?)(?=^\[|\Z)", text, re.MULTILINE | re.DOTALL)
    match = re.search(r"^" + re.escape(key) + r"\s*=\s*(.+)$", body.group(1), re.MULTILINE) if body else None
    return match.group(1).strip() if match else None


def tari_violations(entry, node, wallet):
    found = []
    if not re.search(r"^transport=tcp_tor$", entry, re.MULTILINE) or not re.search(r"^\[ \"\$\{TOR_CLEARNET:-1\}\" = 1 \] \|\| transport=tor$", entry, re.MULTILINE):
        found.append("Tari transport is not tcp_tor with an onion-only tor opt-out")
    if re.search(r"transport=(tcp|memory|socks)\b", entry.replace("transport=tcp_tor", "")):
        found.append("Tari entrypoint can select a direct transport")
    for name, text, section in (("node", node, "base_node.p2p.transport.tor"), ("wallet", wallet, "wallet.p2p.transport.tor")):
        if toml_value(text, section, "proxy_bypass_for_outbound_tcp") != "false":
            found.append(f"Tari {name} can dial TCP peers without Tor")
        if toml_value(text, section, "proxy_bypass_addresses") != "[]":
            found.append(f"Tari {name} bypasses Tor for listed addresses")
        if toml_value(text, section, "socks_address_override") != f'"/ip4/{TOR_IP}/tcp/9050"':
            found.append(f"Tari {name} SOCKS proxy is not the Tor container")
    for name, text in (("node", node), ("wallet", wallet)):
        if toml_value(text, "mainnet.p2p.seeds", "dns_seeds") != "[]":
            found.append(f"Tari {name} resolves DNS seeds")
        if not re.search(r"/onion3/", text) or re.search(r"peer_seeds\s*=\s*\[[^\]]*/(ip4|dns4|tcp)/", text):
            found.append(f"Tari {name} peer seeds are not onion addresses")
        public = toml_value(text, "base_node.p2p" if name == "node" else "wallet.p2p", "public_addresses")
        if public != "[]":
            found.append(f"Tari {name} advertises a public address")
    if toml_value(node, "base_node.p2p.transport", "type") != '"__TRANSPORT__"':
        found.append("Tari node transport is not chosen by the entrypoint")
    if toml_value(wallet, "wallet.p2p.transport", "type") != '"tor"':
        found.append("Tari wallet transport is not tor")
    if toml_value(wallet, "wallet", "use_libtor") != "false":
        found.append("Tari wallet embeds its own Tor")
    for name, text in (("node", node), ("wallet", wallet)):
        if toml_value(text, "auto_update", "check_interval") != "0":
            found.append(f"Tari {name} checks for updates")
    for key in ("control_address", "control_auth"):
        if toml_value(wallet, "wallet.p2p.transport.tor", key) is not None or toml_value(node, "base_node.p2p.transport.tor", key) is not None:
            found.append(f"Tari config sets {key} but the stack opens no Tor control port")
    return found


def zcash_violations(toml, relays):
    found = []
    peers = re.search(r"initial_mainnet_peers\s*=\s*\[(.*?)\]", toml, re.DOTALL)
    peers = re.findall(r'"([^"]+)"', peers.group(1)) if peers else []
    if not peers or set(peers) != {f"{TOR_IP}:{port:d}" for port in relays}:
        found.append(f"Zakura peers are not exactly the Tor SOCKS4A relays: {peers}")
    if re.search(r"initial_testnet_peers|\[network\.(?!x)", toml):
        found.append("Zakura has extra peer lists")
    if toml_value(toml, "network", "listen_addr") != '"127.0.0.1:8233"':
        found.append("Zakura P2P listens beyond loopback")
    if toml_value(toml, "network", "cache_dir") != "false":
        found.append("Zakura caches peers it did not get through Tor")
    if toml_value(toml, "network", "p2p_stack") != '"legacy"':
        found.append("Zakura runs a P2P stack other than the legacy TCP one")
    if re.search(r"^\[(?:metrics|zcashd_compat)\]|^\[network\.zakura\]|opentelemetry", toml, re.MULTILINE):
        found.append("Zakura enables a metrics, zcashd-compat, native P2P or telemetry setting")
    return found


DOCKER = r"(?:docker|\"?\$\{?docker_bin\}?\"?)"
WRAPPER = (
    r"(?:(?:sudo|doas|exec|xargs|env|nohup|timeout|time|nice|ionice|busybox|setsid|stdbuf|flock|unshare)\b\s+"
    r"(?:-\S+\s+(?:[\w./:=-]+\s+)?)*(?:\d+[smhd]?\s+)?"
    r"|(?:" + DOCKER + r"\s+(?:compose\s+)?|compose\s+)exec\s+(?:-\S+\s+(?:\w+\s+)?)*[\w.\-\"$]+\s+"
    r"|[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*"
)
PREFIX = (
    r"(?:^|[|;&(`!]|\$\(|\b(?:then|do|else|if|elif|while|until|RUN)\b|\s-c\s+[\"'])"
    r"\s*" + WRAPPER + r"(?:[^\s\"'|;&()=]*/)?"
)
VERBS = [
    ("curl", r"curl"), ("wget", r"wget"), ("nc", r"(?:nc|ncat|netcat)"), ("socat", r"socat"),
    ("ssh", r"(?:ssh|scp|sftp|rsync)"), ("telnet", r"(?:telnet|ftp)"), ("dns", r"(?:dig|nslookup|host(?!\s+in\b))(?=\s)"),
    ("ping", r"(?:ping|traceroute|mtr|nmap|whois)"), ("browser", r"(?:lynx|links|w3m|aria2c|axel|tftp)"),
    ("openssl", r"openssl\s+s_(?:client|time)"), ("tailscale", r"tailscaled?"),
    ("python-c", r"python3?\s+-c\s+[\"'][^\"']*?\b(?:urllib|urlopen|requests|socket|http\.client|ftplib|smtplib|ssl)"),
    ("gh", r"gh"), ("brew", r"brew\s+(?:install|update|upgrade|tap|fetch|reinstall|bundle)"),
    ("apt", r"(?:apt|apt-get|aptitude|yum|dnf|apk)"), ("pip", r"(?:pip3?|npm|npx|yarn)"),
    ("go", r"go\s+(?:get|install|mod\s+download)"),
    ("git", r"git\s+(?:clone|fetch|pull|push|ls-remote|remote\s+update|submodule)"),
    ("docker pull", DOCKER + r"\s+pull"), ("docker run", DOCKER + r"\s+(?:run|create)"),
    ("docker build", DOCKER + r"\s+(?:build|buildx|login|push|compose\s+(?:build|run|pull))"),
    ("compose pull", r"compose\s+pull"), ("stack pull", r"\"?[^\s\"]*stack\"?\s+pull"), ("compose up", r"compose\s+(?:up|run|build|create)"),
    ("docker up", DOCKER + r"\s+compose\s+up"),
]
URL = re.compile(r"https?://([^/\s\"'?:]+)")


def statements(text):
    """Yield command statements with comments and line continuations removed."""
    joined = re.sub(r"\\\n\s*", " ", text)
    terminator = None
    for line in joined.splitlines():
        if terminator:
            terminator = None if line.strip() == terminator else terminator
            continue
        if not line.strip().startswith("#"):
            yield line
        here = re.search(r"<<-?\s*[\"']?(\w+)[\"']?", line)
        if here:
            terminator = here.group(1)


def compliant(token, line):
    if token == "curl":
        if "--socks5-hostname" in line:
            return bool(re.search(rf"--socks5-hostname\s+[\"']?(\$\{{?TOR_PROXY\}}?|{re.escape(TOR_PROXY)})\b", line))
        urls = URL.findall(line)
        return bool(urls) and all(host in ("127.0.0.1", "localhost", "[::1]") for host in urls)
    if token == "socat":
        return bool(re.search(r"SOCKS4A:127\.0\.0\.1:[a-z0-9.\-]+:\d+,socksport=9050\b|\sTCP4:172\.29\.0\.\d+:\d+(\s|$)", line))
    if token == "nc":
        return "127.0.0.1" in line or "localhost" in line or STACK_SUBNET_PREFIX in line
    if token == "docker run":
        return "--network none" in line
    if token == "compose up":
        return bool(re.search(r"compose\s+up\b", line)) and "--pull never" in line
    return False


def network_hits(text, dockerfile=False):
    hits = []
    for line in statements(text):
        for token, verb in VERBS:
            if re.search(PREFIX + verb + r"(?![\w):-])", line) and not compliant(token, line):
                hits.append(token)
    if re.search(r"/dev/(tcp|udp)/", text):
        hits.append("dev-tcp")
    if re.search(r"^\s*(import|from)\s+(socket|urllib|http\.client|requests|ftplib|smtplib|ssl)\b", text, re.MULTILINE):
        hits.append("python-net")
    return hits


CLASSES = {"install-time", "dev-tool", "release-ci"}


def parse_allowlist(text):
    entries = {}
    for number, raw in enumerate(text.splitlines(), 1):
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        fields = [f.strip() for f in raw.split("|")]
        if len(fields) != 5 or fields[3] not in CLASSES or not fields[4] or not fields[2].isdigit():
            raise ValueError(f"allowlist line {number:d} needs: path | token | count | class | reason")
        key = (fields[0], fields[1])
        if key in entries:
            raise ValueError(f"allowlist line {number:d} repeats {key}")
        entries[key] = int(fields[2])
    return entries


def scan_paths():
    patterns = [
        "stack", "configure.sh", "scripts/*", "packaging/postinst", "packaging/systemd/*",
        "build/*/*.sh", "build/*/Dockerfile", "build/tor/entrypoint.sh", "docker-compose*.yml", "Formula/*.rb*",
    ]
    seen = set()
    for pattern in patterns:
        for path in sorted(ROOT.glob(pattern)):
            if path.is_file() and path not in seen:
                seen.add(path)
                yield path.relative_to(ROOT).as_posix(), path.read_text()


def network_violations(sources, allowlist):
    counts = {}
    for path, text in sources:
        for token in network_hits(text):
            counts[(path, token)] = counts.get((path, token), 0) + 1
    found = []
    for key, count in sorted(counts.items()):
        if allowlist.get(key) != count:
            found.append(f"{key[0]} uses {key[1]} x{count:d}, allowlist has {allowlist.get(key)}")
    for key in sorted(set(allowlist) - set(counts)):
        found.append(f"stale allowlist entry {key}")
    return found

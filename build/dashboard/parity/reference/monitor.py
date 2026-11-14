"""Lightweight operational alerts for a single node.

Telegram gets a short message on real state transitions (debounced, one per
incident); Healthchecks.io gets a periodic dead-man ping so an outside service
notices when this whole box goes dark — the one failure a monitor running on
the box can never report. Both are off until configured, and both ride the
stack's Tor SOCKS proxy so neither is a clearnet beacon.
"""
import os
import re
import shutil
import time

import requests

TELEGRAM_BOT_TOKEN = os.environ.get("TELEGRAM_BOT_TOKEN", "")
TELEGRAM_CHAT_ID = os.environ.get("TELEGRAM_CHAT_ID", "")
HEALTHCHECKS_URL = os.environ.get("HEALTHCHECKS_URL", "").strip().rstrip("/")
NODE_NAME = os.environ.get("NODE_NAME", "bitcoin-node")
# New-block alerts are opt-in even when Telegram is on — a synced node finds
# ~144 blocks a day, which is a lot of pings for most operators.
ALERT_NEW_BLOCK = os.environ.get("ALERT_NEW_BLOCK", "") not in ("", "0", "false", "False")

TOR_PROXY = "socks5h://172.29.0.25:9050"  # socks5h: DNS resolves through Tor too
PROXIES = {"http": TOR_PROXY, "https": TOR_PROXY}

STACK_VERSION = os.environ.get("STACK_VERSION", "")
STACK_RELEASES_URL = "https://api.github.com/repos/VijitSingh97/node-manager/releases/latest"
CORE_RELEASES_URL = "https://api.github.com/repos/bitcoin/bitcoin/releases/latest"

EVENTS_LOG = os.environ.get("EVENTS_LOG", "/state/events.log")

TICK_SECONDS = 60
PING_EVERY_TICKS = 5             # healthchecks cadence: every 5 minutes
DOWN_AFTER_TICKS = 3             # debounce: this many consecutive RPC failures = down
UPDATE_CHECK_EVERY_TICKS = 1440  # daily
DISK_WARN_FREE_GB = 50

# Set by check_updates, shown as a badge in the dashboard footer
update_available = ""


def log_event(text):
    """Append a timestamped line to the persistent event log on /state, so
    incidents (down/recovered, ping failures, monitor restarts) survive
    container restarts and reboots — docker logs don't."""
    print(text)  # keep them in docker logs too
    try:
        # ponytail: naive rotation — over 1 MB, keep the newest half
        if os.path.exists(EVENTS_LOG) and os.path.getsize(EVENTS_LOG) > 1_000_000:
            with open(EVENTS_LOG) as f:
                lines = f.readlines()
            with open(EVENTS_LOG, "w") as f:
                f.writelines(lines[len(lines) // 2:])
        with open(EVENTS_LOG, "a") as f:
            f.write(time.strftime("%Y-%m-%dT%H:%M:%SZ ", time.gmtime()) + text + "\n")
    except Exception as e:
        print(f"Event log write failed: {e}")


def send_telegram(text):
    log_event(text)  # every alert-worthy transition is an event worth keeping
    if not (TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID):
        return
    try:
        requests.post(
            f"https://api.telegram.org/bot{TELEGRAM_BOT_TOKEN}/sendMessage",
            json={"chat_id": TELEGRAM_CHAT_ID, "text": f"[{NODE_NAME}] {text}"},
            proxies=PROXIES, timeout=30,
        )
    except Exception as e:
        print(f"Telegram send failed: {e}")


def ping_healthchecks(healthy):
    # Returns True when the ping got through (or there's nothing to do), False
    # when it failed — the caller uses that to retry rather than wait a full
    # cycle.
    if not HEALTHCHECKS_URL:
        return True
    url = HEALTHCHECKS_URL if healthy else HEALTHCHECKS_URL + "/fail"
    try:
        r = requests.get(url, proxies=PROXIES, timeout=10)
        # A bad Tor exit can answer with an error page; without this check that
        # counted as delivered and Healthchecks silently missed the ping.
        if r.status_code != 200:
            log_event(f"healthchecks ping failed: HTTP {r.status_code}")
            return False
        return True
    except Exception as e:
        log_event(f"healthchecks ping failed: {e}")
        return False


def _latest_release_tag(url):
    try:
        r = requests.get(url, proxies=PROXIES, timeout=30,
                         headers={"Accept": "application/vnd.github+json"})
        if r.status_code == 200:
            return (r.json().get("tag_name") or "").lstrip("v")
    except Exception as e:
        print(f"Release check failed for {url}: {e}")
    return ""


def _newer(latest, current):
    """Is `latest` a strictly higher version than `current`? Compares the
    numeric parts, so "31.1" is not newer than "31.1.0" (same release, the
    running one just carries a patch digit) and an *older* tag never alerts."""
    nums = lambda v: [int(x) for x in re.findall(r"\d+", v or "")]
    try:
        return nums(latest) > nums(current)
    except Exception:
        return False


def check_updates(subversion, state):
    """Informational only — a 🆕 alert and a dashboard badge, never an auto-update.
    Alerts only when a strictly newer version exists (never when the box is
    ahead of or level with the latest release)."""
    global update_available
    notes = []

    latest_stack = _latest_release_tag(STACK_RELEASES_URL)
    # Only compare against releases when running one — a dev build's version is
    # "branch-commit", not a version to rank against the latest tag.
    on_release = bool(re.fullmatch(r"\d+\.\d+\.\d+", STACK_VERSION or ""))
    if on_release and latest_stack and _newer(latest_stack, STACK_VERSION):
        notes.append(f"stack v{latest_stack} available (running v{STACK_VERSION})")
        if state.get("notified_stack") != latest_stack:
            send_telegram(f"🆕 stack v{latest_stack} available (running v{STACK_VERSION})")
            state["notified_stack"] = latest_stack

    # subversion looks like /Satoshi:31.1.0/; release tags like v31.1
    current_core = subversion.strip("/").replace("Satoshi:", "")
    latest_core = _latest_release_tag(CORE_RELEASES_URL)
    if current_core and latest_core and _newer(latest_core, current_core):
        notes.append(f"Bitcoin Core {latest_core} available (running {current_core})")
        if state.get("notified_core") != latest_core:
            send_telegram(f"🆕 Bitcoin Core {latest_core} available (running {current_core})")
            state["notified_core"] = latest_core

    update_available = "; ".join(notes)


def tick(get_blockchain_info, state, disk_path="/data", get_network_info=None):
    """One monitoring pass. A small state machine: alerts fire on transitions,
    never repeatedly. Everything observable is injected so tests can drive it."""
    info = get_blockchain_info()
    healthy = info is not None

    if healthy:
        if state.get("down_alerted"):
            send_telegram("🟢 node recovered — RPC is answering again")
            state["down_alerted"] = False
        state["fails"] = 0

        ibd = info.get("initialblockdownload")
        if state.get("ibd") and ibd is False:
            send_telegram(f"✅ initial sync complete at height {info.get('blocks')}")
        state["ibd"] = ibd

        # opt-in new-block alert: only when synced, and skip catch-up bursts
        blocks = info.get("blocks")
        if isinstance(blocks, int):
            prev = state.get("last_block")
            if ALERT_NEW_BLOCK and prev is not None and ibd is False and 0 < blocks - prev <= 6:
                send_telegram(f"🟧 new block {blocks}")
            state["last_block"] = blocks

        free_gb = shutil.disk_usage(disk_path).free / 1024**3
        if free_gb < DISK_WARN_FREE_GB:
            if not state.get("disk_alerted"):
                send_telegram(f"💾 disk low: {free_gb:.0f} GB free")
                state["disk_alerted"] = True
        else:
            state["disk_alerted"] = False
    else:
        state["fails"] = state.get("fails", 0) + 1
        if state["fails"] == DOWN_AFTER_TICKS and not state.get("down_alerted"):
            send_telegram(f"🔴 node down — RPC unreachable for {DOWN_AFTER_TICKS} minutes")
            state["down_alerted"] = True

    # Ping every 5 minutes (matching the recommended Healthchecks period), but
    # keep retrying each tick until one gets through — so a single Tor hiccup
    # can't trip a tight grace window. The clock only advances on success.
    if state.get("ticks", 0) >= state.get("ping_due", 0) and ping_healthchecks(healthy):
        state["ping_due"] = state.get("ticks", 0) + PING_EVERY_TICKS

    if healthy and get_network_info and state.get("ticks", 0) % UPDATE_CHECK_EVERY_TICKS == 0:
        network = get_network_info() or {}
        check_updates(network.get("subversion", ""), state)

    state["ticks"] = state.get("ticks", 0) + 1
    return state


def monitor_loop(get_blockchain_info, get_network_info=None):
    send_telegram("🚀 node monitor online")
    state = {}
    while True:
        try:
            tick(get_blockchain_info, state, get_network_info=get_network_info)
        except Exception as e:
            print(f"Monitor tick failed: {e}")
        time.sleep(TICK_SECONDS)

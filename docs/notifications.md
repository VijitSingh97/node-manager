# Notifications & Remote Access

## Dashboard settings

The gear at the bottom right opens node installation, launch at boot/login,
Appearance, Dashboard password, Telegram, and Healthchecks. Each tower contains
its own separate Alerts and LAN access sections under Node controls. Changes apply
immediately unless LAN port settings change. A dashboard password protects the
gear and the rest of the dashboard. The gear works from localhost, a
configured private LAN address, or the Tor onion dashboard (enable
`dashboard.onion` in `config.json` and open the address shown by `./stack doctor`
in Tor Browser). Use the configured dashboard password for every access path.

Dashboard changes are written atomically to `notification_settings.json` in the
persistent `dashboard_state` volume, with owner-only permissions. They survive
reboots, container recreation, and upgrades, and the existing stack backup/restore
includes them. Saved overrides take priority over the notification values in
`config.json`. **Use server config** removes the override and restores those
values. After editing the server file, apply it and reset the override if needed.

The gear displays the saved bot token and Healthchecks ping URL so they can be
edited directly. Anyone with dashboard access can view those values. Disabling a
service clears its credentials from the dashboard override; the server
configuration is unchanged.

Each configured tower's LAN access section shows whether RPC or ZMQ is exposed.
With the installed host helper, its Save button can open or close ports using the
server's configured private LAN address. The port number is editable; the
address stays in server configuration. Port edits stay local until Save. The button reads
**Save & restart** when a port change will recreate the Tor relay and dashboard;
the blockchain node processes keep running. The helper validates the address,
port, enabled node, saved credentials, and actual host/Docker port conflicts
before updating `config.json` or restarting services. A conflict names the
selected listener and leaves the edits available for correction.

Three opt-in ways to keep an eye on the node set when you're off the LAN:
Telegram alerts, a Healthchecks.io dead-man's switch, and dashboard access
over Tor. All three are off until configured, and every outbound
notification rides the stack's Tor proxy (`socks5h`, so even DNS resolves
through Tor) — enabling them never turns the node into a clearnet beacon.

Every value below goes in `config.json` under `notifications` (or
`dashboard`); see [Configuration](configuration.md) for the full key table.
Apply changes with `./stack apply`.

---

## Telegram alerts

A one-way pager: short messages on real state transitions, debounced so a
momentary blip never pings you and each incident fires exactly once. Nothing
controls the node over Telegram — there are no commands, so a leaked chat can
read alerts but never change anything.

| Alert | When it fires |
|---|---|
| 🚀 **Monitor online** | The dashboard started with an already-synced node — confirms the bot works after setup. |
| 🔴 **Node down** | One configured node's RPC is unreachable for 3 consecutive minutes. |
| 🟢 **Node recovered** | That node's RPC answers again after a down alert. |
| ✅ **Sync complete** | A node's initial sync finished. |
| 💾 **Disk low** | A node's data volume has less than 50 GB free. Re-arms if space is freed. |
| 🆕 **Update available** | A new stack release exists (checked daily, over Tor). Also a dashboard badge. Informational only; update manually through APT, Homebrew, or [GitHub](operations.md#upgrading). |
| 🟧 **New block** | A synced node accepted a new block. **Off by default**; Bitcoin produces about 144 a day and Monero about 720. Enable either node separately. |

Every message is prefixed with the host's name (`NODE_NAME`, the box's
hostname by default), and node alerts identify Bitcoin or Monero. Several hosts
and both chains can safely share one chat.

Automatic Telegram alerts stay quiet while each node performs its initial sync.
When a chain finishes, the bot sends one chain-labelled ✅ sync-complete
message; normal down, disk, and optional new-block alerts then begin for it.
Bitcoin and Monero keep separate incident state in the persistent dashboard
volume, so a dashboard restart does not duplicate an alert or confuse recovery.

### 1. Create a bot and get its token

Message [@BotFather](https://t.me/BotFather), send `/newbot`, and follow the
prompts. It replies with a **bot token** like `1234567890:AAF...`.

### 2. Get your chat id

Create a group (or use a direct chat), add your bot to it, and send it any
message — a bot can't start a conversation, so it must be spoken to first.
Then visit `https://api.telegram.org/bot<token>/getUpdates` and read the
`chat.id`. A group id is negative (e.g. `-1001234567890`); a direct-chat id
is positive.

### 3. Add both to `config.json`

```json
"notifications": {
    "telegram_bot_token": "1234567890:AAF...",
    "telegram_chat_id": "-1001234567890",
    "alert_new_block": false,
    "alert_new_block_monero": false
}
```

`./stack apply`, and an already-synced node sends the 🚀 message within a
minute. A new node stays quiet until it sends ✅ sync complete.

> NOTE: The bot token can send messages as your bot if leaked, but it can't
> read other chats or touch the node. Server-configured tokens live in the
> gitignored configuration and environment; dashboard overrides live in the
> private persistent settings JSON. Backups contain these credentials too.

---

## Healthchecks.io dead-man's switch

Telegram alerts come *from* the box, so the one failure they can never report
is the box itself going dark — power loss, a kernel panic, a dead NIC, the
whole host hanging. A dead machine can't send its own "I'm down".

[Healthchecks.io](https://healthchecks.io/) inverts the logic: the stack
pings a unique URL on a schedule, and **their** servers alert you when the
pings *stop*. The check runs off-box, so it survives the very outage you want
to catch. Silence is the alarm.

### 1. Create a check

Sign up (the free tier is plenty for one node) and create a check. Set its
schedule to match the stack's fixed **5-minute** ping:

- **Period — 5 minutes.** How often Healthchecks.io expects a ping.
- **Grace — 1–2 minutes.** How long a ping can be late before you're
  alerted. With a 5-minute period you're paged after roughly **6–7 minutes**
  of true silence. The stack retries a failed ping every minute until one
  gets through, so a brief Tor blip won't false-alarm even on a tight grace;
  widen the grace if you'd rather tolerate longer restarts.

Copy the check's **ping URL** — it looks like `https://hc-ping.com/<uuid>`.

### 2. Route the alert somewhere

On the check's **Integrations** tab, point it at email, Telegram, Slack, a
webhook — whatever you'll see. If you set up the Telegram bot above, send
Healthchecks.io to the **same** group so host-down and in-stack alerts land
together.

### 3. Add the ping URL to `config.json`

```json
"notifications": {
    "healthchecks_url": "https://hc-ping.com/your-uuid-here"
}
```

`./stack apply`. The first ping fires within a minute, so the check's **Last
Ping** flips from *Never* to a timestamp almost immediately — that's your
confirmation it works.

The one Healthchecks URL represents the whole configured node set. It receives a
normal ping only when every enabled node is answering. If Bitcoin or Monero
remains unreachable for three checks while the box is alive, the stack pings
the check's `/fail` endpoint instead, so Healthchecks shows *down* rather than
*late*. Disabled nodes are excluded. A live node that is still performing its
initial sync remains healthy for this aggregate check; its operational Telegram
alerts stay suppressed until sync completes.

---

## Dashboard over Tor

Reach the dashboard from anywhere — phone on cellular, another city — with no
port-forwarding, via a Tor onion service:

```json
"dashboard": {
    "password": "pickapassword1",
    "onion": true
}
```

`./stack apply`, then find the dashboard onion address in the doctor report
(use `bitcoin-stack` instead of `./stack` for a package installation):

```bash
./stack doctor
```

Open that `.onion` in [Tor Browser](https://www.torproject.org/). The address
is unguessable (an ed25519 key), but treat it as a secret. A non-empty
`dashboard.password` is required; `configure.sh` rejects onion publication
without one. The onion adds no host port; the ordinary dashboard port keeps its
configured loopback or private-LAN bind.

> NOTE: The onion keys live in the `tor_data` volume. Recreating it (e.g.
> `docker compose down -v`) generates a **new** address — back it up with
> `./stack backup` after enabling.

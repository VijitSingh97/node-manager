#!/usr/bin/env python3
import argparse
import base64
import datetime
import json
import os
import re
import shutil
import ssl
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

OWNER = "VijitSingh97"
REPO = "node-manager"
APP_SLUG = "bitcoin-starter-stack-developer"
API = "https://api.github.com"
PERMISSIONS = {
    "contents": "write",
    "issues": "write",
    "metadata": "read",
    "pull_requests": "write",
    "workflows": "write",
}
TOKEN_ENV = (
    "BSS_DEVELOPER_BOT_ASKPASS",
    "BSS_DEVELOPER_BOT_TOKEN",
    "GH_ENTERPRISE_TOKEN",
    "GH_TOKEN",
    "GITHUB_ENTERPRISE_TOKEN",
    "GITHUB_TOKEN",
)
TRANSPORT_ENV = (
    "ALL_PROXY",
    "CURL_CA_BUNDLE",
    "HTTPS_PROXY",
    "HTTP_PROXY",
    "NO_PROXY",
    "REQUESTS_CA_BUNDLE",
    "SSL_CERT_DIR",
    "SSL_CERT_FILE",
    "SSLKEYLOGFILE",
    "all_proxy",
    "https_proxy",
    "http_proxy",
    "no_proxy",
)


class BotError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        return None


def build_http():
    saved = {name: os.environ.pop(name) for name in TRANSPORT_ENV if name in os.environ}
    try:
        context = ssl.create_default_context()
    finally:
        os.environ.update(saved)
    return urllib.request.build_opener(
        urllib.request.ProxyHandler({}),
        urllib.request.HTTPSHandler(context=context),
        NoRedirect,
    )


HTTP = build_http()


def config_dir():
    return Path(os.environ.get("BSS_DEVELOPER_BOT_CONFIG_DIR", "~/.config/bitcoin-starter-stack-developer")).expanduser()


def check_private(path, kind):
    try:
        info = path.stat()
    except OSError:
        raise BotError(f"{kind} not found: {path}") from None
    if path.is_symlink() or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
        raise BotError(f"{kind} must be owned by the current user and inaccessible to other users: {path}")


def require_tool(name):
    path = shutil.which(name)
    if not path:
        raise BotError(f"required executable not found: {name}")
    return path


def configure(app_id, source):
    if app_id <= 0:
        raise BotError("App ID must be a positive integer")
    source = source.expanduser().resolve()
    if not source.is_file():
        raise BotError(f"private key not found: {source}")
    openssl = require_tool("openssl")
    checked = subprocess.run(
        [openssl, "pkey", "-in", str(source), "-noout"],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    if checked.returncode:
        raise BotError("OpenSSL could not read the private key")

    directory = config_dir()
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(directory, 0o700)
    check_private(directory, "config directory")
    key = directory / "private-key.pem"
    config = directory / "config.json"
    with tempfile.NamedTemporaryFile(dir=directory, delete=False) as tmp:
        tmp.write(source.read_bytes())
        tmp_path = Path(tmp.name)
    os.chmod(tmp_path, 0o600)
    os.replace(tmp_path, key)
    config.write_text(json.dumps({"app_id": app_id}) + "\n", encoding="utf-8")
    os.chmod(config, 0o600)
    check_private(key, "private key")
    check_private(config, "config file")
    print(f"Configured {APP_SLUG} in {directory}")


def load_config():
    directory = config_dir()
    check_private(directory, "config directory")
    config = directory / "config.json"
    key = Path(os.environ.get("BSS_DEVELOPER_BOT_PRIVATE_KEY", directory / "private-key.pem")).expanduser()
    check_private(config, "config file")
    check_private(key, "private key")
    try:
        app_id = int(os.environ.get("BSS_DEVELOPER_BOT_APP_ID") or json.loads(config.read_text())["app_id"])
    except (OSError, ValueError, KeyError, TypeError, json.JSONDecodeError) as exc:
        raise BotError("invalid developer bot configuration") from exc
    if app_id <= 0:
        raise BotError("App ID must be a positive integer")
    return app_id, key


def b64(data):
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def mint_jwt(app_id, key, now=None):
    now = int(time.time() if now is None else now)
    header = b64(json.dumps({"alg": "RS256", "typ": "JWT"}, separators=(",", ":")).encode())
    payload = b64(json.dumps({"iat": now - 60, "exp": now + 540, "iss": str(app_id)}, separators=(",", ":")).encode())
    signed = f"{header}.{payload}".encode()
    result = subprocess.run(
        [require_tool("openssl"), "dgst", "-sha256", "-sign", str(key)],
        input=signed,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    if result.returncode or not result.stdout:
        raise BotError("OpenSSL could not sign the GitHub App JWT")
    return f"{signed.decode()}.{b64(result.stdout)}"


def request_json(path, token, method="GET", payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(
        API + path,
        data=data,
        method=method,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with HTTP.open(request, timeout=20) as response:
            return json.load(response)
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        status = getattr(exc, "code", "network error")
        raise BotError(f"GitHub API request failed ({status})") from None


def installation_identity():
    app_id, key = load_config()
    jwt = mint_jwt(app_id, key)
    app = request_json("/app", jwt)
    if app.get("id") != app_id or app.get("slug") != APP_SLUG:
        raise BotError(f"App ID does not identify {APP_SLUG}")
    installation = request_json(f"/repos/{OWNER}/{REPO}/installation", jwt)
    if installation.get("app_id") != app_id or installation.get("account", {}).get("login", "").casefold() != OWNER.casefold():
        raise BotError("App installation does not belong to the expected owner")
    installation_id = installation.get("id")
    if not isinstance(installation_id, int):
        raise BotError("GitHub returned an invalid installation")
    issued = request_json(
        f"/app/installations/{installation_id}/access_tokens",
        jwt,
        "POST",
        {"repositories": [REPO], "permissions": PERMISSIONS},
    )
    repos = [item.get("full_name", "").casefold() for item in issued.get("repositories", [])]
    if repos != [f"{OWNER}/{REPO}".casefold()] or issued.get("permissions") != PERMISSIONS:
        raise BotError("GitHub did not limit the token to the expected repository and permissions")
    token = issued.get("token")
    if not isinstance(token, str) or not token:
        raise BotError("GitHub returned an invalid installation token")
    try:
        expires = datetime.datetime.fromisoformat(issued["expires_at"].replace("Z", "+00:00")).timestamp()
    except (KeyError, TypeError, ValueError):
        raise BotError("GitHub returned an invalid token expiry") from None
    if expires <= time.time() or expires > time.time() + 3900:
        raise BotError("GitHub returned an unexpected token lifetime")
    login = f"{APP_SLUG}[bot]"
    bot = request_json(f"/users/{urllib.parse.quote(login)}", token)
    if bot.get("login", "").casefold() != login.casefold() or bot.get("type") != "Bot" or not isinstance(bot.get("id"), int):
        raise BotError("GitHub returned an invalid App bot identity")
    email = f"{bot['id']}+{bot['login']}@users.noreply.github.com"
    return token, bot["login"], email


def child_env():
    env = os.environ.copy()
    for name in tuple(env):
        if name in TOKEN_ENV or name in TRANSPORT_ENV or name.startswith(("GH_", "GIT_")):
            env.pop(name)
    return env


def reject_unsafe_repo_config(git, env):
    configured = subprocess.run(
        [git, "config", "--local", "--includes", "--name-only", "--list"],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )
    if configured.returncode:
        raise BotError("could not inspect repository Git transport configuration")
    for key in configured.stdout.splitlines():
        lowered = key.casefold()
        if lowered.startswith(("http.", "credential.")) or (
            lowered.startswith("url.") and lowered.endswith((".insteadof", ".pushinsteadof"))
        ):
            raise BotError("repository Git transport overrides are disabled")


def bot_commit_args(args, name, email):
    caller = args[1:]
    before_paths = caller[:caller.index("--")] if "--" in caller else caller
    blocked = ("--author", "--reset-author")
    for arg in before_paths:
        option = arg.split("=", 1)[0]
        if len(option) > 2 and any(danger.startswith(option) for danger in blocked):
            raise BotError("commit author overrides are disabled")
    return ["commit", f"--author={name} <{email}>", *caller]


def run_gh(args, token):
    if not args or args[0] not in {"api", "pr", "release", "run", "workflow"}:
        raise BotError("gh command must be one of: api, pr, release, run, workflow")
    if any(
        "://" in arg
        or arg.startswith(("//", "-R", "--repo=", "--debug=", "--verbose=", "--hostname=", "--host="))
        or arg in {"-R", "--repo", "--debug", "--verbose", "--hostname", "--host"}
        for arg in args
    ):
        raise BotError("debug and alternate-host options are disabled")
    env = child_env()
    env.update({"GH_TOKEN": token, "GH_HOST": "github.com", "GH_REPO": f"{OWNER}/{REPO}"})
    with tempfile.TemporaryDirectory() as gh_config:
        env["GH_CONFIG_DIR"] = gh_config
        return subprocess.run([require_tool("gh"), *args], env=env, check=False).returncode


def run_git(args, token, name, email):
    if not args or args[0] not in {"commit", "push"}:
        raise BotError("git command must be commit or push")
    commit_args = bot_commit_args(args, name, email) if args[0] == "commit" else None
    env = child_env()
    env.update({
        "GIT_AUTHOR_NAME": name,
        "GIT_AUTHOR_EMAIL": email,
        "GIT_COMMITTER_NAME": name,
        "GIT_COMMITTER_EMAIL": email,
    })
    git = require_tool("git")
    env.update({"GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"})
    origin = subprocess.run(
        [git, "config", "--get", "remote.origin.url"],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )
    allowed_origins = {
        f"https://github.com/{OWNER}/{REPO}.git",
        f"https://github.com/{OWNER}/{REPO}",
        f"git@github.com:{OWNER}/{REPO}.git",
        f"git@github.com:{OWNER}/{REPO}",
    }
    if origin.returncode or origin.stdout.strip() not in allowed_origins:
        raise BotError("current checkout is not the expected GitHub repository")
    if commit_args:
        return subprocess.run([git, *commit_args], env=env, check=False).returncode
    if any("://" in arg or "@github.com" in arg or arg.startswith(("--exec", "--receive-pack")) for arg in args[1:]):
        raise BotError("push arguments may contain only options and refspecs")
    reject_unsafe_repo_config(git, env)
    target = f"https://github.com/{OWNER}/{REPO}.git"
    effective = subprocess.run(
        [git, "ls-remote", "--get-url", target],
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        check=False,
    )
    if effective.returncode or effective.stdout.strip() != target:
        raise BotError("could not verify Git URL rewrite configuration")
    env.update({
        "BSS_DEVELOPER_BOT_ASKPASS": "1",
        "BSS_DEVELOPER_BOT_TOKEN": token,
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_ASKPASS": str(Path(__file__).resolve()),
    })
    command = [
        git,
        "-c", "credential.helper=",
        "-c", "credential.useHttpPath=true",
        "-c", "core.hooksPath=/dev/null",
        "-c", "http.sslVerify=true",
        "-c", "http.followRedirects=false",
        "push", target, *args[1:],
    ]
    return subprocess.run(command, env=env, check=False).returncode


def askpass(prompt):
    match = re.search(r"'(https://[^']+)'", prompt)
    if not match:
        return 1
    parsed = urllib.parse.urlsplit(match.group(1))
    expected_path = f"/{OWNER}/{REPO}.git"
    try:
        valid_target = parsed.hostname == "github.com" and parsed.port is None
    except ValueError:
        return 1
    if not valid_target or parsed.path.rstrip("/") != expected_path or parsed.query or parsed.fragment:
        return 1
    if parsed.username not in (None, "x-access-token") or parsed.password is not None:
        return 1
    if prompt.lower().startswith("username"):
        print("x-access-token")
    elif prompt.lower().startswith("password"):
        token = os.environ.get("BSS_DEVELOPER_BOT_TOKEN")
        if not token:
            return 1
        print(token)
    else:
        return 1
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=f"Run repository writes as {APP_SLUG}")
    sub = parser.add_subparsers(dest="command", required=True)
    setup = sub.add_parser("configure")
    setup.add_argument("app_id", type=int)
    setup.add_argument("private_key", type=Path)
    for command in ("gh", "git"):
        child = sub.add_parser(command)
        child.add_argument("args", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    if args.command == "configure":
        configure(args.app_id, args.private_key)
        return 0
    token, name, email = installation_identity()
    return run_gh(args.args, token) if args.command == "gh" else run_git(args.args, token, name, email)


if __name__ == "__main__":
    if os.environ.get("BSS_DEVELOPER_BOT_ASKPASS") == "1":
        raise SystemExit(askpass(sys.argv[1] if len(sys.argv) > 1 else ""))
    try:
        raise SystemExit(main())
    except BotError as exc:
        print(f"developer-bot: {exc}", file=sys.stderr)
        raise SystemExit(1)

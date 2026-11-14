#!/usr/bin/env python3
import argparse
import json
import os
import re
import subprocess
import tempfile
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BITCOINCASH_SIGNER = "D465135F97D0047E18E99DC321810A542031C02C"
DOGECOIN_SIGNER = "DC6EF4A8BF9F1B1E4DE1EE522D3A345B98D0DC1F"
LITECOIN_SIGNER = "D35621D53A1CC6A3456758D03620E9D387E55666"
MONERO_SIGNER = "81AC591FE9C4B65C5806AFC3F0AF4D462A0BDF92"
USER_AGENT = "node-starter-stack-dependency-updater"
ALLOWED_HOSTS = {
    "api.github.com",
    "github.com",
    "gitlab.com",
    "objects.githubusercontent.com",
    "release-assets.githubusercontent.com",
    "www.getmonero.org",
}


class UpdateError(Exception):
    pass


def trusted_origin(url):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != "https" or parsed.hostname not in ALLOWED_HOSTS or parsed.username or parsed.password:
        raise UpdateError("refusing dependency metadata from an unexpected origin")
    try:
        port = parsed.port
    except ValueError as exc:
        raise UpdateError("refusing dependency metadata from an unexpected origin") from exc
    if port not in (None, 443):
        raise UpdateError("refusing dependency metadata from an unexpected origin")
    return parsed.scheme, parsed.hostname, port or 443


class SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        old_origin = trusted_origin(request.full_url)
        new_origin = trusted_origin(new_url)
        redirected = super().redirect_request(request, fp, code, message, headers, new_url)
        if old_origin != new_origin:
            redirected.remove_header("Authorization")
        return redirected


HTTP = urllib.request.build_opener(SafeRedirect)


def fetch(url):
    _scheme, host, _port = trusted_origin(url)
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": USER_AGENT,
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if host == "api.github.com" and os.environ.get("GITHUB_TOKEN"):
        headers["Authorization"] = f"Bearer {os.environ['GITHUB_TOKEN']}"
    request = urllib.request.Request(
        url,
        headers=headers,
    )
    with HTTP.open(request, timeout=30) as response:
        trusted_origin(response.url)
        return response.read()


def releases(repo, get=fetch):
    items = json.loads(get(f"https://api.github.com/repos/{repo}/releases?per_page=20"))
    if not isinstance(items, list):
        raise UpdateError("GitHub releases response is not a list")
    return items


def stable_release(items):
    for item in items:
        if not item.get("draft") and not item.get("prerelease"):
            tag = item.get("tag_name", "")
            if re.fullmatch(r"v?\d+(?:\.\d+)+", tag):
                return item, tag.removeprefix("v")
    raise UpdateError("no stable release found")


def asset(release, name):
    matches = [item for item in release.get("assets", []) if item.get("name") == name]
    if len(matches) != 1:
        raise UpdateError(f"release must contain exactly one {name} asset")
    return matches[0]


def verify_signed_checksums(data, key, signer, project):
    with tempfile.TemporaryDirectory() as directory:
        home = Path(directory)
        signed = home / "SHA256SUMS.asc"
        signed.write_bytes(data)
        imported = subprocess.run(
            ["gpg", "--batch", "--homedir", str(home), "--import", str(key)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if imported.returncode:
            raise UpdateError(f"could not import the pinned {project} release key")
        checked = subprocess.run(
            ["gpg", "--batch", "--homedir", str(home), "--status-fd=1", "--verify", str(signed)],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            check=False,
        )
    valid = [line.split()[2] for line in checked.stdout.splitlines() if line.startswith("[GNUPG:] VALIDSIG ")]
    if checked.returncode or valid != [signer]:
        raise UpdateError(f"{project} checksum signature does not match the pinned release key")


def verify_dogecoin_checksums(data):
    verify_signed_checksums(data, ROOT / "tools/keys/dogecoin-release.asc", DOGECOIN_SIGNER, "Dogecoin")


def verify_bitcoincash_checksums(data):
    verify_signed_checksums(
        data,
        ROOT / "tools/keys/bitcoincash-release.asc",
        BITCOINCASH_SIGNER,
        "Bitcoin Cash",
    )


def verify_litecoin_checksums(data):
    verify_signed_checksums(data, ROOT / "tools/keys/litecoin-release.asc", LITECOIN_SIGNER, "Litecoin")


def verify_monero_checksums(data):
    verify_signed_checksums(data, ROOT / "tools/keys/monero-release.asc", MONERO_SIGNER, "Monero")


def signed_digests(data, verify=verify_dogecoin_checksums):
    verify(data)
    text = data.decode("utf-8")
    pairs = re.findall(r"^([0-9a-f]{64})  ([^\s]+)$", text, re.MULTILINE)
    if len({name for _digest, name in pairs}) != len(pairs):
        raise UpdateError("signed checksums contain a duplicate filename")
    return {name: digest for digest, name in pairs}


def version_tuple(version):
    return tuple(int(part) for part in version.split("."))


def current_version(text, project):
    if project == "litecoin":
        match = re.search(r"releases/download/v([0-9.]+)/litecoin-", text)
    else:
        match = re.search(r"releases/download/v([0-9.]+)/dogecoin-", text)
    if not match:
        raise UpdateError(f"could not find the current {project} version")
    return match.group(1)


def replace_release(text, project, old, new, amd64, arm64):
    if version_tuple(new) < version_tuple(old):
        raise UpdateError(f"refusing to downgrade {project} from {old} to {new}")
    updated = text.replace(f"{project}-{old}", f"{project}-{new}")
    updated, amd_count = re.subn(r"(amd64\) archive=x86_64-linux-gnu; sha=)[0-9a-f]{64}", rf"\g<1>{amd64}", updated)
    updated, arm_count = re.subn(r"(arm64\) archive=aarch64-linux-gnu; sha=)[0-9a-f]{64}", rf"\g<1>{arm64}", updated)
    if amd_count != 1 or arm_count != 1:
        raise UpdateError(f"unexpected {project} checksum layout")
    return updated


def litecoin_update(text, get=fetch, verify=verify_litecoin_checksums):
    release, version = stable_release(releases("litecoin-project/litecoin", get))
    amd_name = f"litecoin-{version}-x86_64-linux-gnu.tar.gz"
    arm_name = f"litecoin-{version}-aarch64-linux-gnu.tar.gz"
    sums_asset = asset(release, "SHA256SUMS.asc")
    sums_url = sums_asset.get("browser_download_url")
    if not isinstance(sums_url, str):
        raise UpdateError("Litecoin checksum asset has no download URL")
    sums = signed_digests(get(sums_url), verify)
    if amd_name not in sums or arm_name not in sums:
        raise UpdateError("signed Litecoin checksums are missing a required Linux archive")
    return replace_release(
        text,
        "litecoin",
        current_version(text, "litecoin"),
        version,
        sums[amd_name],
        sums[arm_name],
    )


def dogecoin_update(text, get=fetch, verify=verify_dogecoin_checksums):
    release, version = stable_release(releases("dogecoin/dogecoin", get))
    sums_asset = asset(release, "SHA256SUMS.asc")
    sums_url = sums_asset.get("browser_download_url")
    if not isinstance(sums_url, str):
        raise UpdateError("Dogecoin checksum asset has no download URL")
    sums = signed_digests(get(sums_url), verify)
    amd_name = f"dogecoin-{version}-x86_64-linux-gnu.tar.gz"
    arm_name = f"dogecoin-{version}-aarch64-linux-gnu.tar.gz"
    if amd_name not in sums or arm_name not in sums:
        raise UpdateError("signed Dogecoin checksums are missing a required Linux archive")
    return replace_release(
        text,
        "dogecoin",
        current_version(text, "dogecoin"),
        version,
        sums[amd_name],
        sums[arm_name],
    )


def bitcoincash_update(text, get=fetch, verify=verify_bitcoincash_checksums):
    _release, version = stable_release(releases("bitcoin-cash-node/bitcoin-cash-node", get))
    sums_url = (
        "https://gitlab.com/bitcoin-cash-node/announcements/-/raw/master/"
        f"release-sigs/{version}/SHA256SUMS.{version}.asc.Calin_Culianu"
    )
    sums = signed_digests(get(sums_url), verify)
    amd_name = f"bitcoin-cash-node-{version}-x86_64-linux-gnu.tar.gz"
    arm_name = f"bitcoin-cash-node-{version}-aarch64-linux-gnu.tar.gz"
    if amd_name not in sums or arm_name not in sums:
        raise UpdateError("signed Bitcoin Cash checksums are missing a required Linux archive")
    current = re.search(r"releases/download/v([0-9.]+)/bitcoin-cash-node-", text)
    if not current:
        raise UpdateError("could not find the current Bitcoin Cash version")
    return replace_release(
        text,
        "bitcoin-cash-node",
        current.group(1),
        version,
        sums[amd_name],
        sums[arm_name],
    )


def monero_update(text, get=fetch, verify=verify_monero_checksums):
    sums = signed_digests(get("https://www.getmonero.org/downloads/hashes.txt"), verify)
    versions = {
        match.group(1)
        for name in sums
        if (match := re.fullmatch(r"monero-linux-(?:x64|armv8)-v(\d+(?:\.\d+)+)\.tar\.bz2", name))
    }
    if len(versions) != 1:
        raise UpdateError("signed Monero checksums do not identify one Linux release")
    version = versions.pop()
    current = re.search(r"^ARG MONERO_VERSION=([0-9.]+)$", text, re.MULTILINE)
    if not current:
        raise UpdateError("could not find the current Monero version")
    if version_tuple(version) < version_tuple(current.group(1)):
        raise UpdateError(f"refusing to downgrade monero from {current.group(1)} to {version}")
    names = {
        "MONERO_AMD64_SHA256": f"monero-linux-x64-v{version}.tar.bz2",
        "MONERO_ARM64_SHA256": f"monero-linux-armv8-v{version}.tar.bz2",
    }
    updated, count = re.subn(r"(^ARG MONERO_VERSION=)[0-9.]+$", rf"\g<1>{version}", text, flags=re.MULTILINE)
    for variable, name in names.items():
        updated, replaced = re.subn(
            rf"(^ARG {variable}=)[0-9a-f]{{64}}$",
            rf"\g<1>{sums[name]}",
            updated,
            flags=re.MULTILINE,
        )
        count += replaced
    if count != 3:
        raise UpdateError("unexpected Monero version or checksum layout")
    return updated


def update(path, transform, apply):
    original = path.read_text(encoding="utf-8")
    changed = transform(original)
    if changed == original:
        return False
    if apply:
        path.write_text(changed, encoding="utf-8")
    return True


def main():
    parser = argparse.ArgumentParser(description="Update pinned node release binaries")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    changed = []
    for relative, transform in (
        ("build/monero/Dockerfile", monero_update),
        ("build/litecoin/Dockerfile", litecoin_update),
        ("build/dogecoin/Dockerfile", dogecoin_update),
        ("build/bitcoincash/Dockerfile", bitcoincash_update),
    ):
        if update(ROOT / relative, transform, args.apply):
            changed.append(relative)
    if changed:
        print(("updated " if args.apply else "updates available for ") + ", ".join(changed))
        return 0
    print("node release pins are current")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (UpdateError, OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"node dependency update failed: {exc}", file=os.sys.stderr)
        raise SystemExit(1)

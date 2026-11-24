#!/usr/bin/env python3
import argparse
import difflib
import functools
import io
import json
import lzma
import os
import re
import subprocess
import sys
import tarfile
import tempfile
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PROJECTS = {
    "litecoin": {
        "repo": "litecoin-project/litecoin",
        "signer": "D35621D53A1CC6A3456758D03620E9D387E55666",
        "key": "litecoin-release.asc",
        "label": "Litecoin",
        "stage": "litecoin",
    },
    "dogecoin": {
        "repo": "dogecoin/dogecoin",
        "signer": "DC6EF4A8BF9F1B1E4DE1EE522D3A345B98D0DC1F",
        "key": "dogecoin-release.asc",
        "label": "Dogecoin",
        "stage": "dogecoin",
    },
    "bitcoin-cash-node": {
        "repo": "bitcoin-cash-node/bitcoin-cash-node",
        "signer": "D465135F97D0047E18E99DC321810A542031C02C",
        "key": "bitcoincash-release.asc",
        "label": "Bitcoin Cash",
        "stage": "bitcoincash",
        "sums_url": (
            "https://gitlab.com/bitcoin-cash-node/announcements/-/raw/master/"
            "release-sigs/{version}/SHA256SUMS.{version}.asc.Calin_Culianu"
        ),
    },
    "monero": {
        "signer": "81AC591FE9C4B65C5806AFC3F0AF4D462A0BDF92",
        "key": "monero-release.asc",
        "label": "Monero",
    },
}
USER_AGENT = "node-starter-stack-dependency-updater"
ALLOWED_HOSTS = {
    "api.github.com",
    "deb.debian.org",
    "dl-cdn.alpinelinux.org",
    "github.com",
    "gitlab.com",
    "objects.githubusercontent.com",
    "release-assets.githubusercontent.com",
    "www.getmonero.org",
}
ALPINE_ARCHES = ("x86_64", "aarch64")
DEBIAN_ARCHES = ("amd64", "arm64")


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


def verifier(name):
    project = PROJECTS[name]
    return functools.partial(
        verify_signed_checksums, key=ROOT / "tools/keys" / project["key"], signer=project["signer"], project=project["label"]
    )


def signed_digests(data, verify):
    verify(data)
    text = data.decode("utf-8")
    pairs = re.findall(r"^([0-9a-f]{64})  ([^\s]+)$", text, re.MULTILINE)
    if len({name for _digest, name in pairs}) != len(pairs):
        raise UpdateError("signed checksums contain a duplicate filename")
    return {name: digest for digest, name in pairs}


def version_tuple(version):
    return tuple(int(part) for part in version.split("."))


def current_version(text, project):
    match = re.search(rf"releases/download/v([0-9.]+)/{project}-", text)
    if not match:
        raise UpdateError(f"could not find the current {project} version")
    return match.group(1)


def refuse_downgrade(project, old, new):
    if version_tuple(new) < version_tuple(old):
        raise UpdateError(f"refusing to downgrade {project} from {old} to {new}")


def replace_release(text, stage, project, old, new, amd64, arm64):
    refuse_downgrade(project, old, new)
    match = re.search(rf"^FROM \S+ AS {stage}$.*?(?=^FROM |\Z)", text, re.MULTILINE | re.DOTALL)
    if not match:
        raise UpdateError(f"could not find the {stage} build stage")
    updated = match[0].replace(f"/download/v{old}/", f"/download/v{new}/").replace(f"{project}-{old}", f"{project}-{new}")
    updated, amd_count = re.subn(r"(amd64\) archive=x86_64-linux-gnu; sha=)[0-9a-f]{64}", rf"\g<1>{amd64}", updated)
    updated, arm_count = re.subn(r"(arm64\) archive=aarch64-linux-gnu; sha=)[0-9a-f]{64}", rf"\g<1>{arm64}", updated)
    if amd_count != 1 or arm_count != 1:
        raise UpdateError(f"unexpected {project} checksum layout")
    return text[: match.start()] + updated + text[match.end() :]


def github_release_update(text, name, get=fetch, verify=None):
    project = PROJECTS[name]
    label = project["label"]
    release, version = stable_release(releases(project["repo"], get))
    if "sums_url" in project:
        sums_url = project["sums_url"].format(version=version)
    else:
        sums_url = asset(release, "SHA256SUMS.asc").get("browser_download_url")
        if not isinstance(sums_url, str):
            raise UpdateError(f"{label} checksum asset has no download URL")
    sums = signed_digests(get(sums_url), verify or verifier(name))
    amd_name = f"{name}-{version}-x86_64-linux-gnu.tar.gz"
    arm_name = f"{name}-{version}-aarch64-linux-gnu.tar.gz"
    if amd_name not in sums or arm_name not in sums:
        raise UpdateError(f"signed {label} checksums are missing a required Linux archive")
    return replace_release(
        text, project["stage"], name, current_version(text, name), version, sums[amd_name], sums[arm_name]
    )


def monero_update(text, get=fetch, verify=None):
    sums = signed_digests(get("https://www.getmonero.org/downloads/hashes.txt"), verify or verifier("monero"))
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
    refuse_downgrade("monero", current.group(1), version)
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


def index_versions(text, name_field, version_field):
    fields = re.compile(rf"^({name_field}|{version_field}): ?(\S+)$", re.MULTILINE)
    versions = {}
    for stanza in text.split("\n\n"):
        values = dict(fields.findall(stanza))
        if name_field in values and version_field in values:
            versions.setdefault(values[name_field], set()).add(values[version_field])
    return versions


@functools.cache
def alpine_index(branch, arch, get=fetch):
    texts = []
    for repository in ("main", "community"):
        data = get(f"https://dl-cdn.alpinelinux.org/alpine/v{branch}/{repository}/{arch}/APKINDEX.tar.gz")
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
            texts.append(archive.extractfile("APKINDEX").read().decode("utf-8"))
    return index_versions("\n\n".join(texts), "P", "V")


@functools.cache
def debian_index(codename, arch, get=fetch):
    texts = [
        lzma.decompress(get(f"https://deb.debian.org/{archive}/dists/{suite}/main/binary-{arch}/Packages.xz")).decode(
            "utf-8"
        )
        for archive, suite in (
            ("debian", codename),
            ("debian", f"{codename}-updates"),
            ("debian-security", f"{codename}-security"),
        )
    ]
    return index_versions("\n\n".join(texts), "Package", "Version")


def debian_part_key(part):
    return [
        ([-1 if char == "~" else ord(char) if char.isalpha() else ord(char) + 256 for char in letters] + [0], int(digits or 0))
        for letters, digits in re.findall(r"([^0-9]*)([0-9]*)", part)
    ]


def debian_version_key(version):
    epoch, upstream, revision = re.fullmatch(r"(?:([0-9]+):)?(.+?)(?:-([^-]+))?", version).groups()
    return int(epoch or 0), debian_part_key(upstream), debian_part_key(revision or "")


def only_version(versions, name):
    if len(versions) != 1:
        raise UpdateError(f"package index lists several {name} versions")
    return next(iter(versions))


def newest_debian_version(versions, _name):
    return max(versions, key=debian_version_key)


def bump_pins(text, command, indexes, pick):
    def newest(pin):
        name = pin[2]
        chosen = set()
        for index in indexes:
            if name not in index:
                raise UpdateError(f"package index has no {name}")
            chosen.add(pick(index[name], name))
        if len(chosen) != 1:
            raise UpdateError(f"architectures disagree on the newest {name}")
        return f"{pin[1]}{name}={chosen.pop()}"

    def bump(match):
        updated, pins = re.subn(r"(\s)([a-z0-9][a-z0-9+.-]*)=\S+", newest, match[0])
        if not pins:
            raise UpdateError("package install command has no version pins")
        return updated

    updated, commands = re.subn(command, bump, text)
    if commands != 1:
        raise UpdateError("expected exactly one pinned package install command")
    return updated


def alpine_pins_update(text, get=fetch):
    branch = re.search(r"^FROM alpine:([0-9]+\.[0-9]+)(?:\.[0-9]+)?@sha256:", text, re.MULTILINE)
    if not branch:
        raise UpdateError("could not find the Alpine branch")
    indexes = [alpine_index(branch[1], arch, get) for arch in ALPINE_ARCHES]
    return bump_pins(text, r"apk add [^\n]*", indexes, only_version)


def debian_pins_update(text, get=fetch):
    codename = re.search(r"^FROM debian:([a-z]+)(?:-slim)?@sha256:", text, re.MULTILINE)
    if not codename:
        raise UpdateError("could not find the Debian release")
    indexes = [debian_index(codename[1], arch, get) for arch in DEBIAN_ARCHES]
    return bump_pins(text, r"apt-get install [^&\n]*", indexes, newest_debian_version)


def changes(old, new):
    old, new = old.split(), new.split()
    for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(None, old, new, autojunk=False).get_opcodes():
        if tag != "equal":
            yield f"{' '.join(old[i1:i2])} -> {' '.join(new[j1:j2])}"


def update(path, transform, apply):
    original = path.read_text(encoding="utf-8")
    changed = transform(original)
    if apply and changed != original:
        path.write_text(changed, encoding="utf-8")
    return list(changes(original, changed))


def main():
    parser = argparse.ArgumentParser(description="Update pinned node release binaries and OS package pins")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    for relative, transform in (
        ("build/monero/Dockerfile", monero_update),
        ("build/bitcoin-family/Dockerfile", lambda text: debian_pins_update(
                github_release_update(github_release_update(github_release_update(text, "litecoin"), "dogecoin"), "bitcoin-cash-node")
            )),
        ("build/tor/Dockerfile", alpine_pins_update),
    ):
        print(f"{relative}: {'; '.join(update(ROOT / relative, transform, args.apply)) or 'current'}", flush=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (UpdateError, OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"node dependency update failed: {exc}", file=sys.stderr)
        raise SystemExit(1)

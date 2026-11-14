import importlib.util
import io
import json
import lzma
import re
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.request
from pathlib import Path


ROOT = Path(__file__).parents[1]
SPEC = importlib.util.spec_from_file_location("node_dependencies", ROOT / "tools/update-node-dependencies.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def response(items):
    return json.dumps(items).encode()


def apkindex(*packages):
    data = "".join(f"C:Q1fixture=\nP:{name}\nV:{version}\nA:x86_64\n\n" for name, version in packages).encode()
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        info = tarfile.TarInfo("APKINDEX")
        info.size = len(data)
        archive.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


def debian_packages(*packages):
    return lzma.compress(
        "".join(
            f"Package: {name}\nSource: fixture\nVersion: {version}\nDescription: fixture\n Version: 999\n\n"
            for name, version in packages
        ).encode()
    )


def changed_lines(old, new):
    return [line for line, updated in zip(old.splitlines(), new.splitlines()) if line != updated]


class NodeDependencyTests(unittest.TestCase):
    def test_stable_release_excludes_drafts_prereleases_and_invalid_tags(self):
        release, version = MODULE.stable_release(
            [
                {"tag_name": "v4.0.0", "draft": True, "prerelease": False},
                {"tag_name": "v3.0.0-rc1", "draft": False, "prerelease": True},
                {"tag_name": "nightly", "draft": False, "prerelease": False},
                {"tag_name": "v2.1.0", "draft": False, "prerelease": False},
            ]
        )
        self.assertEqual(version, "2.1.0")
        self.assertEqual(release["tag_name"], "v2.1.0")

    def test_litecoin_uses_signed_checksums(self):
        text = (ROOT / "build/litecoin/Dockerfile").read_text()
        release = {
            "tag_name": "v0.21.5.8",
            "draft": False,
            "prerelease": False,
            "assets": [{"name": "SHA256SUMS.asc", "browser_download_url": "https://github.com/checksums"}],
        }
        sums = (
            ("a" * 64) + "  litecoin-0.21.5.8-x86_64-linux-gnu.tar.gz\n"
            + ("b" * 64) + "  litecoin-0.21.5.8-aarch64-linux-gnu.tar.gz\n"
        ).encode()

        def get(url):
            return response([release]) if "api.github.com" in url else sums

        verified = []
        updated = MODULE.litecoin_update(text, get, lambda data: verified.append(data))
        self.assertEqual(verified, [sums])
        self.assertIn("litecoin-0.21.5.8", updated)
        self.assertIn("sha=" + "a" * 64, updated)
        self.assertIn("sha=" + "b" * 64, updated)
        self.assertNotIn("litecoin-0.21.4", updated)

    def test_litecoin_rejects_bad_checksum_signature(self):
        text = (ROOT / "build/litecoin/Dockerfile").read_text()
        release = {
            "tag_name": "v0.21.5.8",
            "draft": False,
            "prerelease": False,
            "assets": [{"name": "SHA256SUMS.asc", "browser_download_url": "https://github.com/checksums"}],
        }
        sums = (
            ("a" * 64) + "  litecoin-0.21.5.8-x86_64-linux-gnu.tar.gz\n"
            + ("b" * 64) + "  litecoin-0.21.5.8-aarch64-linux-gnu.tar.gz\n"
        ).encode()

        def get(url):
            return response([release]) if "api.github.com" in url else sums

        with self.assertRaises(MODULE.UpdateError):
            MODULE.litecoin_update(
                text,
                get,
                lambda _data: (_ for _ in ()).throw(MODULE.UpdateError("bad signature")),
            )

    def test_dogecoin_requires_signed_checksums(self):
        text = (ROOT / "build/dogecoin/Dockerfile").read_text()
        release = {
            "tag_name": "v1.15.0",
            "draft": False,
            "prerelease": False,
            "assets": [{"name": "SHA256SUMS.asc", "browser_download_url": "https://github.com/checksums"}],
        }
        sums = (
            ("c" * 64) + "  dogecoin-1.15.0-x86_64-linux-gnu.tar.gz\n"
            + ("d" * 64) + "  dogecoin-1.15.0-aarch64-linux-gnu.tar.gz\n"
        ).encode()

        def get(url):
            return response([release]) if "api.github.com" in url else sums

        with self.assertRaises(MODULE.UpdateError):
            MODULE.dogecoin_update(text, get, lambda _data: (_ for _ in ()).throw(MODULE.UpdateError("bad signature")))
        updated = MODULE.dogecoin_update(text, get, lambda _data: None)
        self.assertIn("dogecoin-1.15.0", updated)
        self.assertIn("sha=" + "c" * 64, updated)
        self.assertIn("sha=" + "d" * 64, updated)

    def test_update_dry_run_reports_without_writing(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "Dockerfile"
            path.write_text("RUN apk add tor=1-r0 socat=2-r0")
            report = MODULE.update(path, lambda text: text.replace("tor=1-r0", "tor=1-r1"), False)
            self.assertEqual(report, ["tor=1-r0 -> tor=1-r1"])
            self.assertEqual(path.read_text(), "RUN apk add tor=1-r0 socat=2-r0")
            self.assertEqual(MODULE.update(path, lambda text: text, True), [])

    def test_debian_versions_sort_like_dpkg(self):
        ordered = [
            "1.0~rc1",
            "1.0",
            "1.0-1",
            "1.0-1+deb12u5",
            "1.0-1+deb12u15",
            "1.0a",
            "1.0.1",
            "1:0.1",
        ]
        self.assertEqual(sorted(reversed(ordered), key=MODULE.debian_version_key), ordered)

    def test_debian_pins_take_the_newest_version_across_suites(self):
        text = (ROOT / "build/bitcoincash/Dockerfile").read_text()
        suites = {
            "main": debian_packages(("curl", "7.88.1-10+deb12u99"), ("libatomic1", "12.2.0-14+deb12u2")),
            "updates": debian_packages(("ca-certificates", "20250419~deb12u3")),
            "security": debian_packages(("curl", "7.88.1-10+deb12u100"), ("libatomic1", "12.2.0-14+deb12u1")),
        }
        urls = []

        def get(url):
            urls.append(url)
            return suites["security" if "-security/" in url else "updates" if "-updates/" in url else "main"]

        updated = MODULE.debian_pins_update(text, get)
        self.assertEqual(len(urls), 6)
        self.assertTrue(all(url.startswith("https://deb.debian.org/") for url in urls))
        for pin in ("ca-certificates=20250419~deb12u3", "curl=7.88.1-10+deb12u100", "libatomic1=12.2.0-14+deb12u2"):
            self.assertIn(pin, updated)
        changed = changed_lines(text, updated)
        self.assertEqual(len(changed), 1)
        self.assertIn("apt-get install", changed[0])

    def test_alpine_pins_follow_the_branch_index(self):
        text = (ROOT / "build/tor/Dockerfile").read_text()
        repositories = {
            "main": apkindex(("tor", "0.4.9.99-r0"), ("socat", "9.9-r1")),
            "community": apkindex(("netcat-openbsd", "9.9-r2")),
        }
        urls = []

        def get(url):
            urls.append(url)
            return repositories[url.split("/")[5]]

        updated = MODULE.alpine_pins_update(text, get)
        self.assertEqual({url.split("/")[6] for url in urls}, {"x86_64", "aarch64"})
        self.assertIn("apk add --no-cache tor=0.4.9.99-r0 netcat-openbsd=9.9-r2 socat=9.9-r1", updated)
        self.assertEqual(len(changed_lines(text, updated)), 1)

    def test_package_pin_lookups_fail_loudly(self):
        text = (ROOT / "build/tor/Dockerfile").read_text()
        complete = apkindex(("tor", "1-r0"), ("netcat-openbsd", "1-r0"), ("socat", "1-r0"))
        cases = {
            "missing package": lambda _url: apkindex(("tor", "1-r0")),
            "architectures disagree": lambda url: (
                complete if "x86_64" in url else apkindex(("tor", "2-r0"), ("netcat-openbsd", "1-r0"), ("socat", "1-r0"))
            ),
            "several versions": lambda url: complete if "/main/" in url else apkindex(("tor", "2-r0")),
        }
        for case, get in cases.items():
            with self.subTest(case), self.assertRaises(MODULE.UpdateError):
                MODULE.alpine_pins_update(text, get)
        with self.assertRaises(MODULE.UpdateError):
            MODULE.alpine_pins_update(re.sub(r"=\S+", "", text), lambda _url: complete)

    def test_metadata_fetch_rejects_unexpected_hosts_before_connecting(self):
        with self.assertRaises(MODULE.UpdateError):
            MODULE.fetch("https://example.com/releases.json")

    def test_token_only_goes_to_github_api(self):
        class Response:
            def __init__(self, url):
                self.url = url

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def read(self):
                return b"{}"

        class HTTP:
            def __init__(self):
                self.requests = []

            def open(self, request, timeout):
                self.requests.append((request, timeout))
                return Response(request.full_url)

        fake = HTTP()
        with mock.patch.object(MODULE, "HTTP", fake), mock.patch.dict("os.environ", {"GITHUB_TOKEN": "test-token"}):
            MODULE.fetch("https://api.github.com/repos/example/releases")
            MODULE.fetch("https://github.com/example/SHA256SUMS.asc")
            MODULE.fetch("https://www.getmonero.org/downloads/hashes.txt")
            MODULE.fetch("https://deb.debian.org/debian/dists/bookworm/main/binary-amd64/Packages.xz")
            MODULE.fetch("https://dl-cdn.alpinelinux.org/alpine/v3.24/main/x86_64/APKINDEX.tar.gz")
        self.assertEqual(fake.requests[0][0].get_header("Authorization"), "Bearer test-token")
        for request, _timeout in fake.requests[1:]:
            self.assertIsNone(request.get_header("Authorization"))

    def test_cross_origin_redirect_strips_authorization(self):
        request = urllib.request.Request(
            "https://api.github.com/repos/example/releases",
            headers={"Authorization": "Bearer test-token"},
        )
        redirected = MODULE.SafeRedirect().redirect_request(
            request,
            None,
            302,
            "Found",
            {},
            "https://release-assets.githubusercontent.com/asset",
        )
        self.assertIsNone(redirected.get_header("Authorization"))

    def test_redirect_rejects_non_https_origin(self):
        request = urllib.request.Request("https://api.github.com/repos/example/releases")
        with self.assertRaises(MODULE.UpdateError):
            MODULE.SafeRedirect().redirect_request(
                request,
                None,
                302,
                "Found",
                {},
                "http://api.github.com/repos/example/releases",
            )

    def test_monero_uses_signed_hashes_for_both_architectures(self):
        text = (ROOT / "build/monero/Dockerfile").read_text()
        sums = (
            ("e" * 64) + "  monero-linux-x64-v0.18.6.0.tar.bz2\n"
            + ("f" * 64) + "  monero-linux-armv8-v0.18.6.0.tar.bz2\n"
        ).encode()
        verified = []
        updated = MODULE.monero_update(text, lambda _url: sums, lambda data: verified.append(data))
        self.assertEqual(verified, [sums])
        self.assertIn("ARG MONERO_VERSION=0.18.6.0", updated)
        self.assertIn("ARG MONERO_AMD64_SHA256=" + "e" * 64, updated)
        self.assertIn("ARG MONERO_ARM64_SHA256=" + "f" * 64, updated)

    def test_bitcoincash_uses_signed_checksums(self):
        text = (ROOT / "build/bitcoincash/Dockerfile").read_text()
        release = {"tag_name": "v30.0.0", "draft": False, "prerelease": False, "assets": []}
        sums = (
            ("1" * 64) + "  bitcoin-cash-node-30.0.0-x86_64-linux-gnu.tar.gz\n"
            + ("2" * 64) + "  bitcoin-cash-node-30.0.0-aarch64-linux-gnu.tar.gz\n"
        ).encode()

        def get(url):
            return response([release]) if "api.github.com" in url else sums

        verified = []
        updated = MODULE.bitcoincash_update(text, get, lambda data: verified.append(data))
        self.assertEqual(verified, [sums])
        self.assertIn("bitcoin-cash-node-30.0.0", updated)
        self.assertIn("sha=" + "1" * 64, updated)
        self.assertIn("sha=" + "2" * 64, updated)

    def test_direct_node_images_are_digest_pinned_and_compose_monitored(self):
        compose = (ROOT / "docker-compose.yml").read_text()
        for image in ("bitcoin/bitcoin", "ghcr.io/tari-project/minotari_node", "zakuracore/zakura"):
            self.assertRegex(compose, rf"image: {re.escape(image)}:[^\s]+@sha256:[0-9a-f]{{64}}")
        dependabot = (ROOT / ".github/dependabot.yml").read_text()
        self.assertRegex(dependabot, r"package-ecosystem: docker-compose\s+directory: /")
        for directory in ("monero", "litecoin", "dogecoin", "bitcoincash"):
            self.assertRegex(dependabot, rf"package-ecosystem: docker\s+directory: /build/{directory}\b")


if __name__ == "__main__":
    unittest.main()

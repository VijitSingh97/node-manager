import base64
import datetime
import importlib.util
import io
import json
import os
import subprocess
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from unittest import mock

SCRIPT = Path(__file__).parents[1] / "tools" / "developer-bot.py"
SPEC = importlib.util.spec_from_file_location("developer_bot", SCRIPT)
BOT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BOT)


class Response(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *unused):
        self.close()


class DeveloperBotTest(unittest.TestCase):
    def git(self, repo, *args, env=None):
        return subprocess.run(
            ["git", "-C", str(repo), *args],
            check=True,
            env=env,
            capture_output=True,
            text=True,
        ).stdout.strip()

    def human_repo(self, repo):
        self.git(repo, "init", "-q")
        self.git(repo, "remote", "add", "origin", f"https://github.com/{BOT.OWNER}/{BOT.REPO}.git")
        self.git(repo, "config", "user.name", "Human Author")
        self.git(repo, "config", "user.email", "human@example.com")
        (repo / "tracked.txt").write_text("one\n", encoding="utf-8")
        self.git(repo, "add", "tracked.txt")
        self.git(repo, "commit", "-q", "-m", "human source")

    def test_jwt_is_rs256_and_expires_within_ten_minutes(self):
        signed = None

        def fake_run(args, **kwargs):
            nonlocal signed
            signed = kwargs["input"]
            return subprocess.CompletedProcess(args, 0, b"signature")

        with mock.patch.object(BOT, "require_tool", return_value="/isolated/openssl"), mock.patch.object(BOT.subprocess, "run", side_effect=fake_run):
            token = BOT.mint_jwt(123, Path("/unused/key"), now=1_700_000_000)

        header, claims = signed.decode().split(".")
        decode = lambda value: json.loads(base64.urlsafe_b64decode(value + "=" * (-len(value) % 4)))
        self.assertEqual(decode(header), {"alg": "RS256", "typ": "JWT"})
        self.assertEqual(decode(claims), {"iat": 1_699_999_940, "exp": 1_700_000_540, "iss": "123"})
        self.assertEqual(len(token.split(".")), 3)

    def test_installation_token_is_repo_and_permission_scoped(self):
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=55)).isoformat().replace("+00:00", "Z")
        replies = iter([
            {"id": 123, "slug": BOT.APP_SLUG},
            {"id": 456, "app_id": 123, "account": {"login": BOT.OWNER}},
            {
                "token": "secret-installation-token",
                "expires_at": expiry,
                "permissions": BOT.PERMISSIONS,
                "repositories": [{"full_name": f"{BOT.OWNER}/{BOT.REPO}"}],
            },
            {"id": 789, "login": f"{BOT.APP_SLUG}[bot]", "type": "Bot"},
        ])
        requests = []

        def fake_open(request, timeout):
            requests.append(request)
            return Response(json.dumps(next(replies)).encode())

        with mock.patch.object(BOT, "load_config", return_value=(123, Path("/unused/key"))), mock.patch.object(BOT, "mint_jwt", return_value="jwt"), mock.patch.object(BOT.HTTP, "open", side_effect=fake_open):
            token, name, email = BOT.installation_identity()

        body = json.loads(requests[2].data)
        self.assertEqual(body, {"repositories": [BOT.REPO], "permissions": BOT.PERMISSIONS})
        self.assertEqual(token, "secret-installation-token")
        self.assertEqual(name, f"{BOT.APP_SLUG}[bot]")
        self.assertEqual(email, f"789+{BOT.APP_SLUG}[bot]@users.noreply.github.com")

    def test_wrong_repository_fails_closed_without_printing_token(self):
        expiry = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=55)).isoformat().replace("+00:00", "Z")
        replies = iter([
            {"id": 123, "slug": BOT.APP_SLUG},
            {"id": 456, "app_id": 123, "account": {"login": BOT.OWNER}},
            {"token": "never-print-this", "expires_at": expiry, "permissions": BOT.PERMISSIONS, "repositories": [{"full_name": f"{BOT.OWNER}/other"}]},
        ])

        def fake_open(request, timeout):
            return Response(json.dumps(next(replies)).encode())

        output = io.StringIO()
        with mock.patch.object(BOT, "load_config", return_value=(123, Path("/unused/key"))), mock.patch.object(BOT, "mint_jwt", return_value="jwt"), mock.patch.object(BOT.HTTP, "open", side_effect=fake_open), redirect_stderr(output), self.assertRaises(BOT.BotError):
            BOT.installation_identity()
        self.assertNotIn("never-print-this", output.getvalue())

    def test_gh_uses_isolated_token_and_has_no_real_binary_fallback(self):
        with mock.patch.object(BOT.shutil, "which", return_value=None), mock.patch.object(BOT.subprocess, "run") as run, self.assertRaises(BOT.BotError):
            BOT.run_gh(["pr", "list"], "secret")
        run.assert_not_called()

        seen = {}

        def fake_run(args, **kwargs):
            seen.update(kwargs["env"])
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.object(BOT, "require_tool", return_value="/isolated/gh"), mock.patch.object(BOT.subprocess, "run", side_effect=fake_run), mock.patch.dict(BOT.os.environ, {"GITHUB_TOKEN": "human-token", "GH_DEBUG": "api"}):
            self.assertEqual(BOT.run_gh(["pr", "list"], "app-token"), 0)
        self.assertEqual(seen["GH_TOKEN"], "app-token")
        self.assertNotIn("GITHUB_TOKEN", seen)
        self.assertNotIn("GH_DEBUG", seen)
        self.assertTrue(seen["GH_CONFIG_DIR"].startswith(tempfile.gettempdir()))
        with self.assertRaises(BOT.BotError):
            BOT.run_gh(["api", "https://evil.example/repos/x/y"], "app-token")
        with self.assertRaises(BOT.BotError):
            BOT.run_gh(["pr", "list", "--repo", "evil.example/x/y"], "app-token")

    def test_child_environment_removes_tokens_git_overrides_and_transport_overrides(self):
        unsafe = {
            "BSS_DEVELOPER_BOT_TOKEN": "old-app-token",
            "GH_DEBUG": "api",
            "GITHUB_TOKEN": "human-token",
            "GIT_EXEC_PATH": "/tmp/fake-git-core",
            "GIT_SSL_CAINFO": "/tmp/fake-ca",
            "HTTPS_PROXY": "https://proxy.example",
            "SSL_CERT_FILE": "/tmp/fake-ca",
            "SSLKEYLOGFILE": "/tmp/tls-keys",
        }
        with mock.patch.dict(BOT.os.environ, unsafe):
            clean = BOT.child_env()
        for name in unsafe:
            self.assertNotIn(name, clean)

    def test_git_push_uses_fixed_repo_and_disables_helpers_and_hooks(self):
        calls = []

        def fake_run(args, **kwargs):
            calls.append((args, kwargs))
            if args[1:4] == ["config", "--get", "remote.origin.url"]:
                return subprocess.CompletedProcess(args, 0, f"https://github.com/{BOT.OWNER}/{BOT.REPO}.git\n", "")
            if "--list" in args:
                return subprocess.CompletedProcess(args, 0, "remote.origin.url\n", "")
            if "--get-url" in args:
                return subprocess.CompletedProcess(args, 0, f"https://github.com/{BOT.OWNER}/{BOT.REPO}.git\n", "")
            return subprocess.CompletedProcess(args, 0)

        with mock.patch.object(BOT, "require_tool", return_value="/isolated/git"), mock.patch.object(BOT.subprocess, "run", side_effect=fake_run):
            self.assertEqual(BOT.run_git(["push", "HEAD:main"], "app-token", "bot", "bot@example"), 0)
        call = {"args": calls[-1][0], "env": calls[-1][1]["env"]}
        self.assertIn("credential.helper=", call["args"])
        self.assertIn("credential.useHttpPath=true", call["args"])
        self.assertIn("core.hooksPath=/dev/null", call["args"])
        self.assertIn("http.sslVerify=true", call["args"])
        self.assertIn("http.followRedirects=false", call["args"])
        self.assertIn(f"https://github.com/{BOT.OWNER}/{BOT.REPO}.git", call["args"])
        self.assertNotIn("app-token", " ".join(call["args"]))
        self.assertEqual(call["env"]["BSS_DEVELOPER_BOT_TOKEN"], "app-token")
        self.assertEqual(call["env"]["BSS_DEVELOPER_BOT_ASKPASS"], "1")
        with mock.patch.object(BOT, "require_tool", return_value="/isolated/git"), mock.patch.object(BOT.subprocess, "run", side_effect=fake_run), self.assertRaises(BOT.BotError):
            BOT.run_git(["push", "https://evil.example/repo"], "app-token", "bot", "bot@example")

    def test_git_rejects_wrong_repo_and_url_rewrites(self):
        wrong = subprocess.CompletedProcess([], 0, "https://github.com/example/other.git\n", "")
        with mock.patch.object(BOT, "require_tool", return_value="/isolated/git"), mock.patch.object(BOT.subprocess, "run", return_value=wrong), self.assertRaises(BOT.BotError):
            BOT.run_git(["commit", "-m", "message"], "app-token", "bot", "bot@example")

        def rewritten(args, **kwargs):
            if "remote.origin.url" in args:
                return subprocess.CompletedProcess(args, 0, f"https://github.com/{BOT.OWNER}/{BOT.REPO}.git\n", "")
            if "--list" in args:
                return subprocess.CompletedProcess(args, 0, "remote.origin.url\n", "")
            return subprocess.CompletedProcess(args, 0, "https://evil.example/repo.git\n", "")

        with mock.patch.object(BOT, "require_tool", return_value="/isolated/git"), mock.patch.object(BOT.subprocess, "run", side_effect=rewritten), self.assertRaises(BOT.BotError):
            BOT.run_git(["push", "HEAD:main"], "app-token", "bot", "bot@example")

    def test_git_commit_forces_bot_identity_for_reuse_amend_and_paths(self):
        bot_name = f"{BOT.APP_SLUG}[bot]"
        bot_email = f"789+{bot_name}@users.noreply.github.com"
        cases = (
            ["commit", "-q", "-C", "HEAD"],
            ["commit", "-q", "--amend", "--no-edit"],
            ["commit", "-q", "-m", "path commit", "--", "tracked.txt"],
        )
        for number, command in enumerate(cases):
            with self.subTest(command=command), tempfile.TemporaryDirectory() as directory:
                repo = Path(directory)
                self.human_repo(repo)
                if "--amend" not in command:
                    (repo / "tracked.txt").write_text(f"change {number}\n", encoding="utf-8")
                    self.git(repo, "add", "tracked.txt")
                old = os.getcwd()
                os.chdir(repo)
                try:
                    self.assertEqual(BOT.run_git(command, "unused", bot_name, bot_email), 0)
                finally:
                    os.chdir(old)
                identity = self.git(repo, "show", "-s", "--format=%an%n%ae%n%cn%n%ce").splitlines()
                self.assertEqual(identity, [bot_name, bot_email, bot_name, bot_email])

    def test_git_commit_rejects_author_override_abbreviations(self):
        for option in ("--author=Human <human@example.com>", "--auth=Human <human@example.com>", "--reset-author", "--reset"):
            with self.subTest(option=option), mock.patch.object(BOT, "require_tool") as tool:
                with self.assertRaisesRegex(BOT.BotError, "author overrides"):
                    BOT.run_git(["commit", option, "-m", "message"], "unused", "bot", "bot@example.com")
                tool.assert_not_called()

    def test_git_rejects_local_transport_and_push_rewrite_config(self):
        def configured(key):
            def fake_run(args, **kwargs):
                if args[1:4] == ["config", "--get", "remote.origin.url"]:
                    return subprocess.CompletedProcess(args, 0, f"https://github.com/{BOT.OWNER}/{BOT.REPO}.git\n", "")
                if "--list" in args:
                    return subprocess.CompletedProcess(args, 0, key + "\n", "")
                raise AssertionError(f"unexpected command after unsafe config: {args}")

            return fake_run

        for key in (
            "HTTP.https://github.com/.sslverify",
            "credential.https://github.com.helper",
            "url.https://evil.example/.pushinsteadof",
        ):
            with self.subTest(key=key), mock.patch.object(BOT, "require_tool", return_value="/isolated/git"), mock.patch.object(
                BOT.subprocess, "run", side_effect=configured(key)
            ), self.assertRaisesRegex(BOT.BotError, "transport overrides"):
                BOT.run_git(["push", "HEAD:main"], "app-token", "bot", "bot@example")

    def test_redirects_and_askpass_are_bound_to_exact_repo(self):
        self.assertIsNone(BOT.NoRedirect().redirect_request(None, None, 302, "Found", {}, "https://evil.example"))
        good = f"Password for 'https://x-access-token@github.com/{BOT.OWNER}/{BOT.REPO}.git': "
        output = io.StringIO()
        with mock.patch.dict(BOT.os.environ, {"BSS_DEVELOPER_BOT_TOKEN": "app-token"}), mock.patch("sys.stdout", output):
            self.assertEqual(BOT.askpass(good), 0)
        self.assertEqual(output.getvalue().strip(), "app-token")
        for prompt in (
            f"Password for 'https://attacker@github.com/{BOT.OWNER}/{BOT.REPO}.git': ",
            "Password for 'https://github.com/other/repo.git': ",
            f"Password for 'https://evil.example/{BOT.OWNER}/{BOT.REPO}.git': ",
            "Password for 'https://github.com': ",
        ):
            with self.subTest(prompt=prompt):
                self.assertEqual(BOT.askpass(prompt), 1)

    def test_missing_config_is_a_clean_failure(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(
            BOT.os.environ, {"BSS_DEVELOPER_BOT_CONFIG_DIR": directory}, clear=False
        ), self.assertRaisesRegex(BOT.BotError, "config file not found"):
            BOT.load_config()


if __name__ == "__main__":
    unittest.main()

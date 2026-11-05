import hashlib
import io
from unittest.mock import patch
import urllib.error
import subprocess
import os
import base64
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
import urllib.request

MODULE = Path(__file__).resolve().parents[1] / "sync_gitee_release.py"
spec = importlib.util.spec_from_file_location("mirror", MODULE)
mirror = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mirror)
TAG = "v2.1.2"
COMMIT = "a" * 40


class FakeClient:
    def __init__(self):
        self.originals = {name: ("original:" + name).encode() for name in mirror.names(TAG)}
        self.sums = {name: hashlib.sha256(raw).hexdigest() for name, raw in self.originals.items()}
        self.originals["checksums.txt"] = "".join(f"{self.sums[name]}  {name}\n" for name in sorted(self.sums)).encode()
        self.github = {"tag_name": TAG, "draft": False, "prerelease": False,
            "assets": [{"name": n, "size": len(b), "digest": "sha256:" + hashlib.sha256(b).hexdigest()}
                       for n, b in self.originals.items()]}
        self.commit = COMMIT
        self.gitee_commit = COMMIT
        self.gitee = None
        self.remote = {}
        self.writes = []
        self.reads = []
        self.fail_upload = ""
        self.bad_readback = ""
        self.duplicate_names = False
        self.final_wrong = False

    def get_json(self, url, missing=False):
        self.reads.append(url)
        if "api.github.com" in url and "/releases/" in url:
            return self.github
        if "/git/ref/tags/" in url:
            return {"object": {"type": "commit", "sha": self.commit}}
        if "/tags?page=" in url:
            return [{"name": TAG, "commit": {"sha": self.gitee_commit}}]
        if "/attach_files?" in url:
            result = [{"name": name, "id": i + 1} for i, name in enumerate(self.remote)]
            return result + result[:1] if self.duplicate_names else result
        if "gitee.com/api/" in url and "/releases/tags/" in url:
            return self.gitee
        raise AssertionError("unexpected request " + url)

    def read(self, url, limit=mirror.MAX_JSON, **kwargs):
        self.reads.append(url)
        name = url.rsplit("/", 1)[-1]
        raw = self.originals[name] if "github.com" in url else self.remote[name]
        if "gitee.com" in url and name == self.bad_readback:
            return b"corrupted remote bytes"
        if len(raw) > limit:
            raise mirror.SyncError("oversized")
        return raw

    def authenticated_login(self, token):
        return "fixture-user"

    def form(self, url, fields, token, method="POST", file=None):
        self.writes.append((method, url, fields.copy(), file[0] if file else None))
        if file:
            name, raw = file
            if name == self.fail_upload:
                raise mirror.SyncError("uncertain write")
            self.remote[name] = raw
            return {"name": name, "id": len(self.remote)}
        if method == "PATCH":
            self.gitee["prerelease"] = False
            result = self.gitee.copy()
            if self.final_wrong:
                result["target_commitish"] = "b" * 40
            return result
        self.gitee = {"id": 7, "tag_name": TAG, "target_commitish": COMMIT, "prerelease": True,
                      "name": fields["name"], "body": fields["body"]}
        return self.gitee

    def complete(self, published=True):
        self.gitee = {"id": 7, "tag_name": TAG, "target_commitish": COMMIT, "prerelease": not published,
                      "name": "Existing release title", "body": "Existing release description"}
        self.remote = self.originals.copy()
        self.remote[mirror.MARKER] = mirror.json_bytes({"schemaVersion": 1, "tag": TAG, "commit": COMMIT,
             "checksumsSha256": hashlib.sha256(self.originals["checksums.txt"]).hexdigest(), "assets": self.sums})


class SyncTests(unittest.TestCase):
    def run_sync(self, client, **kwargs):
        def verify_tag(*args, **kwargs):
            if client.gitee_commit != kwargs["commit"]:
                raise mirror.SyncError("Gitee tag commit differs")
        with tempfile.TemporaryDirectory() as temp, patch.object(mirror, "ensure_gitee_tag", side_effect=verify_tag):
            return mirror.synchronize(client, github_repo="official/pipewright", gitee_repo="mirror/pipewright",
                tag=TAG, directory=temp, token="fixture-token", **kwargs)

    def test_original_six_artifacts_marker_last_and_publish(self):
        client = FakeClient()
        result = self.run_sync(client, expected_commit=COMMIT)
        uploads = [w[3] for w in client.writes if w[3]]
        self.assertEqual(len(uploads), 8)
        self.assertEqual(uploads[-1], mirror.MARKER)
        self.assertEqual(client.writes[-1][0], "PATCH")
        self.assertFalse(client.gitee["prerelease"])
        self.assertEqual(result["archives"], 6)
        self.assertEqual(result["result"], "published")
        self.assertEqual(json.loads(client.remote[mirror.MARKER])["assets"], client.sums)

    def test_identical_published_release_is_read_only(self):
        client = FakeClient(); client.complete()
        self.assertEqual(self.run_sync(client)["result"], "already_identical")
        self.assertEqual(client.writes, [])

    def test_partial_staging_resumes_only_missing(self):
        client = FakeClient(); client.complete(published=False)
        del client.remote[mirror.MARKER]
        missing = mirror.names(TAG)[-1]; del client.remote[missing]
        self.run_sync(client)
        self.assertEqual([w[3] for w in client.writes if w[3]], [missing, mirror.MARKER])
        self.assertEqual(client.writes[-1][2]["name"], "Existing release title")
        self.assertEqual(client.writes[-1][2]["body"], "Existing release description")

    def test_mismatching_existing_remote_never_overwritten(self):
        for published in (True, False):
            with self.subTest(published=published):
                client = FakeClient(); client.complete(published)
                client.remote[mirror.names(TAG)[0]] = b"other bytes"
                with self.assertRaises(mirror.SyncError): self.run_sync(client)
                self.assertEqual(client.writes, [])

    def test_published_incomplete_release_never_mutated(self):
        client = FakeClient(); client.complete(); del client.remote[mirror.MARKER]
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertEqual(client.writes, [])

    def test_source_prerelease_or_draft_rejected(self):
        for key in ("prerelease", "draft"):
            client = FakeClient(); client.github[key] = True
            with self.assertRaises(mirror.SyncError): self.run_sync(client)
            self.assertEqual(client.writes, [])

    def test_original_digest_and_size_required(self):
        for change in ("digest", "size"):
            client = FakeClient(); client.github["assets"][0][change] = "invalid"
            with self.assertRaises(mirror.SyncError): self.run_sync(client)
            self.assertEqual(client.writes, [])

    def test_original_checksums_must_match_all_six(self):
        client = FakeClient()
        client.originals[mirror.names(TAG)[0]] = b"altered archive"
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertEqual(client.writes, [])

    def test_checksum_duplicate_or_extra_rejected(self):
        client = FakeClient(); raw = client.originals["checksums.txt"]
        for bad in (raw + raw.splitlines()[0] + b"\n", raw + b"a" * 64 + b"  other.tar.gz\n"):
            with self.assertRaises(mirror.SyncError): mirror.checksum_entries(bad, mirror.names(TAG))

    def test_tag_commit_must_match_upstream_and_gitee(self):
        client = FakeClient()
        with self.assertRaises(mirror.SyncError): self.run_sync(client, expected_commit="b" * 40)
        self.assertEqual(client.writes, [])
        client.gitee_commit = "b" * 40
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertEqual(client.writes, [])

    def test_duplicate_attachment_listing_rejected(self):
        client = FakeClient(); client.complete(); client.duplicate_names = True
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertEqual(client.writes, [])

    def test_uncertain_upload_not_automatically_retried(self):
        client = FakeClient(); client.fail_upload = mirror.names(TAG)[0]
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertEqual(len([w for w in client.writes if w[3] == client.fail_upload]), 1)
        self.assertNotIn(mirror.MARKER, client.remote)
        self.assertFalse(any(w[0] == "PATCH" for w in client.writes))

    def test_readback_failure_blocks_marker_and_publication(self):
        client = FakeClient(); client.bad_readback = mirror.names(TAG)[0]
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertNotIn(mirror.MARKER, client.remote)
        self.assertFalse(any(w[0] == "PATCH" for w in client.writes))

    def test_final_publication_identity_must_match(self):
        client = FakeClient(); client.final_wrong = True
        with self.assertRaises(mirror.SyncError): self.run_sync(client)
        self.assertEqual(len([w for w in client.writes if w[0] == "PATCH"]), 1)

    def test_repository_validation_preserves_legal_forks_only_exact_dot_segments_rejected(self):
        for repo in ("owner/.pipewright", "owner/-pipewright", "owner/pipewright..mirror", ".owner/pipewright"):
            self.assertTrue(mirror.valid_repository(repo), repo)
        for repo in ("./pipewright", "../pipewright", "owner/.", "owner/..", "owner//repo", "https://gitee.com/owner/repo"):
            self.assertFalse(mirror.valid_repository(repo), repo)

    def test_platform_archive_names_match_optional_v_contract(self):
        self.assertEqual(mirror.names("2.1.2"), mirror.names("v2.1.2"))

    def test_bad_repo_and_tag_rejected_before_network(self):
        client = FakeClient()
        for value in ("../bad", "v1.2.3-rc.1", "v1.2.3?token=x"):
            with tempfile.TemporaryDirectory() as temp, self.assertRaises(mirror.SyncError):
                mirror.synchronize(client, github_repo="official/pipewright", gitee_repo="mirror/pipewright",
                    tag=value, directory=temp, token="fixture-token")
        self.assertEqual(client.reads, [])

    def test_redirect_boundaries(self):
        handler = mirror.Redirects()
        for url in ("http://foruda.gitee.com/file", "https://evil.test/file", "https://u:p@foruda.gitee.com/file"):
            with self.assertRaises(mirror.SyncError):
                handler.redirect_request(urllib.request.Request("https://gitee.com/file"), None, 302, "", {}, url)
        with self.assertRaises(mirror.SyncError):
            handler.redirect_request(urllib.request.Request("https://gitee.com/api/v5/upload", data=b"secret", method="POST"),
                None, 307, "", {}, "https://foruda.gitee.com/file")

    def test_form_keeps_token_in_body_and_does_not_follow_write_redirect(self):
        class Reply(io.BytesIO):
            status = 201
        class Opener:
            calls = []
            def open(self, request, timeout):
                self.calls.append(request)
                return Reply(b'{"id":7}')
        opener = Opener()
        with patch("urllib.request.build_opener", return_value=opener):
            client = mirror.Client()
            client.form("https://gitee.com/api/v5/repos/official/pipewright/releases", {"name": "fixture"}, "fixture-secret")
            client.form("https://gitee.com/api/v5/repos/official/pipewright/releases/7/attach_files", {}, "fixture-secret", file=("checksums.txt", b"checksums"))
        self.assertEqual(len(opener.calls), 2)
        for req in opener.calls:
            self.assertNotIn("fixture-secret", req.full_url)
            self.assertIn(b"fixture-secret", req.data)
            self.assertEqual(req.get_method(), "POST")
        self.assertIn("multipart/form-data", opener.calls[1].get_header("Content-type"))
        self.assertIn(b'filename="checksums.txt"', opener.calls[1].data)

    def test_gitee_profile_uses_bearer_header_without_url_token(self):
        class Reply(io.BytesIO): status = 200
        class Opener:
            calls = []
            def open(self, request, timeout):
                self.calls.append(request)
                return Reply(b'{"login":"fixture-user"}')
        opener = Opener()
        with patch("urllib.request.build_opener", return_value=opener):
            self.assertEqual(mirror.Client().authenticated_login("fixture-sensitive-token"), "fixture-user")
        self.assertEqual(opener.calls[0].full_url, "https://gitee.com/api/v5/user")
        self.assertEqual(opener.calls[0].get_header("Authorization"), "Bearer fixture-sensitive-token")
        self.assertIsNone(opener.calls[0].data)

    def test_transport_errors_do_not_print_signed_query_or_remote_body(self):
        class Opener:
            def open(self, request, timeout):
                raise urllib.error.URLError("https://foruda.gitee.com/file?token=fixture-sensitive")
        with patch("urllib.request.build_opener", return_value=Opener()):
            client = mirror.Client()
            with self.assertRaises(mirror.SyncError) as raised:
                client.read("https://gitee.com/official/pipewright/releases/download/v2.1.2/checksums.txt")
            self.assertNotIn("fixture-sensitive", str(raised.exception))
            self.assertIn("uncertain", str(raised.exception))

    def test_json_duplicate_properties_rejected(self):
        with self.assertRaises(mirror.SyncError): mirror.parse_json(b'{"schemaVersion":1,"schemaVersion":2}')


class FakeGit:
    def __init__(self, existing=None, fetched=COMMIT, pushed=COMMIT, fail_push=False):
        self.existing = existing
        self.fetched = fetched
        self.pushed = pushed
        self.fail_push = fail_push
        self.calls = []
        self.did_push = False

    def run(self, args, *, cwd, token="", username="", allowed=(0,)):
        self.calls.append((args, token))
        if args[0] == "ls-remote":
            commit = self.pushed if self.did_push else self.existing
            if commit is None:
                return subprocess.CompletedProcess(args, 2, stdout=b"", stderr=b"")
            ref = "refs/tags/" + TAG
            return subprocess.CompletedProcess(args, 0, stdout=("b" * 40 + "\t" + ref + "\n" + commit + "\t" + ref + "^{}\n").encode(), stderr=b"")
        if "rev-parse" in args:
            return subprocess.CompletedProcess(args, 0, stdout=(self.fetched + "\n").encode(), stderr=b"")
        if "push" in args:
            if self.fail_push:
                raise mirror.SyncError("uncertain push")
            self.did_push = True
        return subprocess.CompletedProcess(args, 0, stdout=b"", stderr=b"")


class GitTagTests(unittest.TestCase):
    def ensure(self, git):
        return mirror.ensure_gitee_tag(git, github_repo="official/pipewright", gitee_repo="mirror/pipewright",
                                      tag=TAG, commit=COMMIT, token="fixture-sensitive-token", username_lookup=lambda _: "fixture-user")

    def test_existing_peeled_commit_is_read_only(self):
        git = FakeGit(existing=COMMIT)
        self.assertEqual(self.ensure(git), "already_present")
        self.assertEqual(len(git.calls), 1)
        self.assertEqual(git.calls[0][1], "")

    def test_existing_different_tag_is_never_replaced(self):
        git = FakeGit(existing="c" * 40)
        with self.assertRaises(mirror.SyncError): self.ensure(git)
        self.assertEqual(len(git.calls), 1)

    def test_only_selected_exact_tag_is_fetched_and_pushed(self):
        git = FakeGit()
        self.assertEqual(self.ensure(git), "pushed")
        pushes = [(args, token) for args, token in git.calls if "push" in args]
        self.assertEqual(len(pushes), 1)
        args, token = pushes[0]
        self.assertEqual(args[-1], "refs/tags/" + TAG + ":refs/tags/" + TAG)
        self.assertNotIn("--force", args); self.assertNotIn("--mirror", args); self.assertNotIn("--tags", args)
        self.assertFalse(any("refs/heads/" in arg for arg in args))
        self.assertEqual(token, "fixture-sensitive-token")
        self.assertTrue(all(not auth for call, auth in git.calls if "push" not in call))
        fetched = [call for call, _ in git.calls if "fetch" in call][0]
        self.assertEqual(fetched[-1], args[-1])
        self.assertNotIn("--depth", fetched)

    def test_moved_github_tag_blocks_push(self):
        git = FakeGit(fetched="c" * 40)
        with self.assertRaises(mirror.SyncError): self.ensure(git)
        self.assertFalse(any("push" in call for call, _ in git.calls))

    def test_uncertain_push_is_not_retried(self):
        git = FakeGit(fail_push=True)
        with self.assertRaises(mirror.SyncError): self.ensure(git)
        self.assertEqual(len([call for call, _ in git.calls if "push" in call]), 1)

    def test_push_requires_final_remote_identity(self):
        git = FakeGit(pushed="c" * 40)
        with self.assertRaises(mirror.SyncError): self.ensure(git)
        self.assertEqual(len([call for call, _ in git.calls if "push" in call]), 1)

    def test_existing_tag_needs_no_authenticated_profile_lookup(self):
        git = FakeGit(existing=COMMIT)
        def forbidden(_): raise AssertionError("existing matching tag must stay read only")
        result = mirror.ensure_gitee_tag(git, github_repo="official/pipewright", gitee_repo="mirror/pipewright",
            tag=TAG, commit=COMMIT, token="", username_lookup=forbidden)
        self.assertEqual(result, "already_present")

    def test_missing_tag_requires_verified_account_login_before_fetch(self):
        git = FakeGit()
        with self.assertRaises(mirror.SyncError):
            mirror.ensure_gitee_tag(git, github_repo="official/pipewright", gitee_repo="mirror/pipewright",
                tag=TAG, commit=COMMIT, token="fixture-sensitive-token", username_lookup=lambda _: "invalid:user")
        self.assertEqual(len(git.calls), 1)

    def test_token_only_in_scoped_memory_header_and_traces_helpers_disabled(self):
        seen = []
        def runner(args, **kwargs):
            seen.append((args, kwargs))
            return subprocess.CompletedProcess(args, 0, stdout=b"ok", stderr=b"")
        with patch.dict(os.environ, {"GIT_TRACE_CURL": "1", "GIT_CONFIG_PARAMETERS": "bad", "GITEE_RELEASE_TOKEN": "fixture-sensitive-token", "GH_TOKEN": "other-fixture-token"}):
            git = mirror.GitCommand(runner)
            git.run(["push", "https://gitee.com/mirror/pipewright.git", "refs/tags/" + TAG + ":refs/tags/" + TAG], cwd=Path.cwd(), token="fixture-sensitive-token", username="fixture-user")
            git.run(["fetch", "https://github.com/official/pipewright.git", "refs/tags/" + TAG], cwd=Path.cwd())
        args, kwargs = seen[0]; env = kwargs["env"]
        self.assertNotIn("fixture-sensitive-token", " ".join(args))
        self.assertNotIn("GIT_TRACE_CURL", env); self.assertNotIn("GIT_CONFIG_PARAMETERS", env)
        self.assertNotIn("GITEE_RELEASE_TOKEN", env); self.assertNotIn("GH_TOKEN", env)
        config = {env[f"GIT_CONFIG_KEY_{i}"]: env[f"GIT_CONFIG_VALUE_{i}"] for i in range(int(env["GIT_CONFIG_COUNT"]))}
        self.assertEqual(config["credential.helper"], "")
        self.assertEqual(config["http.followRedirects"], "false")
        self.assertEqual(config["http.sslVerify"], "true")
        self.assertEqual(env["GIT_TERMINAL_PROMPT"], "0")
        self.assertEqual(env["GIT_CEILING_DIRECTORIES"], str(Path.cwd().resolve()))
        self.assertEqual(config["http.https://gitee.com/.extraHeader"], "Authorization: Basic " + base64.b64encode(b"fixture-user:fixture-sensitive-token").decode())
        self.assertFalse(kwargs["shell"])
        fetch_env = seen[1][1]["env"]
        self.assertFalse(any("Authorization" in val for key, val in fetch_env.items() if key.startswith("GIT_CONFIG_VALUE_")))

    def test_git_errors_never_echo_secret_or_automatically_retry(self):
        calls = []
        def runner(args, **kwargs):
            calls.append(args)
            return subprocess.CompletedProcess(args, 128, stdout=b"", stderr=b"fixture-sensitive-token remote detail")
        with self.assertRaises(mirror.SyncError) as raised:
            mirror.GitCommand(runner).run(["push", "https://gitee.com/mirror/pipewright.git"], cwd=Path.cwd(), token="fixture-sensitive-token", username="fixture-user")
        self.assertNotIn("fixture-sensitive-token", str(raised.exception))
        self.assertEqual(len(calls), 1)

    def test_invalid_tag_identity_fails_before_subprocess(self):
        git = FakeGit()
        with self.assertRaises(mirror.SyncError):
            mirror.ensure_gitee_tag(git, github_repo="official/pipewright", gitee_repo="../bad", tag=TAG, commit=COMMIT, token="fixture-sensitive-token")
        self.assertEqual(git.calls, [])


if __name__ == "__main__": unittest.main()

import contextlib
import itertools
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import tarfile
import unittest
from unittest.mock import patch
import urllib.error
import urllib.parse

SCRIPT = Path(__file__).resolve().parents[2] / "install.sh"
SOURCE = SCRIPT.read_text(encoding="utf-8")
EMBEDDED = SOURCE.split("<<'PY_GITEE_INSTALL'\n", 1)[1].split("\nPY_GITEE_INSTALL", 1)[0]
TAG = "v2.1.2"; COMMIT = "a" * 40


class Response(io.BytesIO):
    status = 200


class Fixture:
    def __init__(self, tag=TAG):
        version = tag.removeprefix("v")
        self.names = {f"pipewright_{version}_{platform}_{cpu}.{'zip' if platform == 'windows' else 'tar.gz'}"
                      for platform in ("darwin", "linux", "windows") for cpu in ("amd64", "arm64")}
        self.archives = {name: ("fixture:" + name).encode() for name in self.names}
        self.sums = {name: hashlib.sha256(raw).hexdigest() for name, raw in self.archives.items()}
        self.checksums = "".join(f"{self.sums[name]}  {name}\n" for name in sorted(self.sums)).encode()
        self.marker = {"schemaVersion": 1, "tag": tag, "commit": COMMIT,
                       "checksumsSha256": hashlib.sha256(self.checksums).hexdigest(), "assets": self.sums.copy()}
        self.metadata = {"tag_name": tag, "target_commitish": COMMIT, "prerelease": False,
                         "assets": [{"name": n, "browser_download_url": "https://ignored.test/never-used"}
                                    for n in self.names.union({"checksums.txt", "pipewright-release.json"})]}
        self.calls = []
        self.broken_json = False
        self.network_error = False
        self.pages = None
        self.fail_page = 0

    def open(self, request, timeout):
        url = request.full_url; self.calls.append(url)
        if self.network_error:
            raise urllib.error.URLError("https://foruda.gitee.com/file?token=must-not-be-printed")
        if "/api/v5/" in url:
            if "?page=" in url:
                page = int(urllib.parse.parse_qs(urllib.parse.urlsplit(url).query)["page"][0])
                if page == self.fail_page:
                    raise urllib.error.URLError("fixture pagination unavailable")
                items = self.pages[page - 1] if self.pages is not None else [self.metadata]
                return Response(json.dumps(items).encode())
            return Response(json.dumps(self.metadata).encode())
        name = url.rsplit("/", 1)[-1]
        if name == "pipewright-release.json":
            return Response(b'{"schemaVersion":1,"schemaVersion":2}' if self.broken_json else json.dumps(self.marker).encode())
        if name == "checksums.txt": return Response(self.checksums)
        return Response(self.archives[name])


class InstallTests(unittest.TestCase):
    def run_embedded(self, fixture, requested="", osname="linux", arch="amd64", repo="official/pipewright", clock=None):
        with tempfile.TemporaryDirectory() as temp:
            out = io.StringIO(); err = io.StringIO(); code = 0
            with patch("urllib.request.build_opener", return_value=fixture), patch.object(sys, "argv", ["-", repo, requested, osname, arch, temp]), contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                with contextlib.ExitStack() as stack:
                    if clock is not None: stack.enter_context(patch("time.monotonic", side_effect=clock))
                    try: exec(compile(EMBEDDED, "install-gitee-embedded", "exec"), {"__name__": "__main__"})
                    except SystemExit as exc: code = exc.code
            files = {p.name: p.read_bytes() for p in Path(temp).iterdir()}
            return code, out.getvalue(), err.getvalue(), files

    def test_valid_latest_downloads_only_selected_platform_after_proof(self):
        fixture = Fixture(); code, out, err, files = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, TAG + "\n")
        self.assertEqual(set(files), {"pipewright_2.1.2_linux_amd64.tar.gz", "checksums.txt"})
        self.assertEqual(len(fixture.calls), 4)
        self.assertTrue(fixture.calls[0].endswith("/releases?page=1&per_page=20"))
        self.assertTrue(all(url.startswith("https://gitee.com/") for url in fixture.calls))
        self.assertFalse(any("ignored.test" in url for url in fixture.calls))

    def test_backfilled_old_release_does_not_regress_highest_stable(self):
        fixture = Fixture()
        older = Fixture("v1.9.9").metadata
        fixture.pages = [[older, fixture.metadata]]
        code, out, err, files = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, TAG + "\n")
        self.assertIn("pipewright_2.1.2_linux_amd64.tar.gz", files)

    def test_numeric_semver_and_equivalent_tag_tie_are_deterministic(self):
        fixture = Fixture("v2.10.0")
        fixture.pages = [[fixture.metadata, Fixture("v2.9.0").metadata]]
        code, out, err, _ = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, "v2.10.0\n")
        fixture = Fixture()
        equivalent = {**fixture.metadata, "tag_name": "2.1.2"}
        fixture.pages = [[equivalent, fixture.metadata]]
        code, out, err, _ = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, TAG + "\n")

    def test_highest_stable_can_be_on_later_page(self):
        fixture = Fixture()
        fixture.pages = [[{"tag_name": f"v1.0.{i}", "prerelease": False} for i in range(20)], [fixture.metadata]]
        code, out, err, _ = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, TAG + "\n")
        self.assertTrue(fixture.calls[1].endswith("?page=2&per_page=20"))

    def test_normal_notes_per_page_fit_existing_response_budget(self):
        fixture = Fixture()
        fixture.metadata["body"] = "n" * (11 * 1024)
        older = [{"tag_name": f"v1.0.{i}", "prerelease": False, "body": "n" * (11 * 1024)} for i in range(19)]
        fixture.pages = [[fixture.metadata, *older], []]
        self.assertLess(len(json.dumps(fixture.pages[0]).encode()), 1024 * 1024)
        code, out, err, files = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, TAG + "\n")
        self.assertIn("pipewright_2.1.2_linux_amd64.tar.gz", files)
        self.assertTrue(fixture.calls[0].endswith("per_page=20"))
        self.assertTrue(fixture.calls[1].endswith("?page=2&per_page=20"))

    def test_incomplete_pagination_never_chooses_available_lower_release(self):
        fixture = Fixture()
        fixture.pages = [[fixture.metadata] + [{"tag_name": f"v1.0.{i}", "prerelease": False} for i in range(19)], []]
        fixture.fail_page = 2
        code, out, _, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(out, ""); self.assertEqual(files, {})
        self.assertEqual(len(fixture.calls), 2)
        self.assertFalse(any("/releases/download/" in url for url in fixture.calls))

    def test_highest_incomplete_stable_never_falls_back_to_complete_older(self):
        fixture = Fixture()
        highest = {"tag_name": "v2.2.0", "prerelease": False, "target_commitish": COMMIT, "assets": []}
        fixture.pages = [[fixture.metadata, highest]]
        code, out, _, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(out, ""); self.assertEqual(files, {})
        self.assertEqual(len(fixture.calls), 1)

    def test_prerelease_and_draft_skipped_before_highest_selection(self):
        fixture = Fixture()
        fixture.pages = [[{"tag_name": "v99.0.0", "prerelease": True},
                          {"tag_name": "v98.0.0", "draft": True, "prerelease": False}, fixture.metadata]]
        code, out, err, _ = self.run_embedded(fixture)
        self.assertEqual(code, 0, err); self.assertEqual(out, TAG + "\n")

    def test_page_limit_and_absolute_budget_fail_closed(self):
        fixture = Fixture(); fixture.pages = [[{"tag_name": f"v1.0.{i}", "prerelease": False} for i in range(20)]] * 100
        code, out, _, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(out, ""); self.assertEqual(files, {})
        self.assertEqual(len(fixture.calls), 100)
        fixture = Fixture()
        code, out, _, files = self.run_embedded(fixture, clock=[0, 301])
        self.assertEqual(code, 1); self.assertEqual(files, {}); self.assertEqual(fixture.calls, [])
        fixture = Fixture()
        code, out, _, files = self.run_embedded(fixture, clock=[0, 0, 301])
        self.assertEqual(code, 1); self.assertEqual(files, {}); self.assertEqual(len(fixture.calls), 1)

    def test_legal_fork_repository_names_and_exact_relative_segments(self):
        for repo in ("owner/.pipewright", "owner/-pipewright", "owner/pipewright..mirror"):
            fixture = Fixture(); code, out, err, _ = self.run_embedded(fixture, requested=TAG, repo=repo)
            self.assertEqual(code, 0, err); self.assertIn("/" + repo + "/releases/tags/", fixture.calls[0])
        for repo in ("./pipewright", "../pipewright", "owner/.", "owner/..", "owner/repo?token=x"):
            fixture = Fixture(); code, _, _, files = self.run_embedded(fixture, repo=repo)
            self.assertEqual(code, 1); self.assertEqual(files, {}); self.assertEqual(fixture.calls, [])

    def test_explicit_version_and_all_install_platforms(self):
        for platform in ("darwin", "linux"):
            for arch in ("amd64", "arm64"):
                fixture = Fixture(); code, out, err, files = self.run_embedded(fixture, TAG, platform, arch)
                self.assertEqual(code, 0, err)
                self.assertTrue(fixture.calls[0].endswith("/tags/" + TAG))
                self.assertIn(f"pipewright_2.1.2_{platform}_{arch}.tar.gz", files)

    def test_optional_v_prefix_matches_backend_contract(self):
        fixture = Fixture(); fixture.metadata["tag_name"] = "2.1.2"; fixture.marker["tag"] = "2.1.2"
        code, out, err, files = self.run_embedded(fixture, "2.1.2")
        self.assertEqual(code, 0, err)
        self.assertEqual(out, "2.1.2\n")
        self.assertIn("pipewright_2.1.2_linux_amd64.tar.gz", files)

    def test_prerelease_or_unmatched_requested_version_rejected(self):
        fixture = Fixture(); fixture.metadata["prerelease"] = True
        code, _, _, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(files, {}); self.assertEqual(len(fixture.calls), 1)
        fixture = Fixture(); code, _, _, files = self.run_embedded(fixture, "v2.1.1")
        self.assertEqual(code, 1); self.assertEqual(files, {})

    def test_incomplete_metadata_blocks_any_package_request(self):
        fixture = Fixture(); fixture.metadata["assets"].pop()
        code, _, _, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(files, {}); self.assertEqual(len(fixture.calls), 1)

    def test_marker_identity_schema_and_six_exact_assets(self):
        mutations = [lambda f: f.marker.update(commit="b" * 40), lambda f: f.marker.update(tag="v2.1.1"),
                     lambda f: f.marker.update(schemaVersion=True), lambda f: f.marker.update(extra="unknown"),
                     lambda f: f.marker["assets"].pop(next(iter(f.marker["assets"])))]
        for mutate in mutations:
            fixture = Fixture(); mutate(fixture)
            code, _, _, files = self.run_embedded(fixture)
            self.assertEqual(code, 1); self.assertEqual(files, {})
            self.assertFalse(any(url.endswith(".tar.gz") for url in fixture.calls))

    def test_duplicate_marker_json_rejected(self):
        fixture = Fixture(); fixture.broken_json = True
        code, _, _, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(files, {})

    def test_checksum_file_and_archive_corruption_rejected(self):
        for target in ("checksums", "archive"):
            fixture = Fixture()
            if target == "checksums": fixture.checksums += b"tamper"
            else: fixture.archives["pipewright_2.1.2_linux_amd64.tar.gz"] = b"tamper"
            code, _, _, files = self.run_embedded(fixture)
            self.assertEqual(code, 1); self.assertEqual(files, {})

    def test_no_cross_source_fallback_or_signed_url_error_leak(self):
        fixture = Fixture(); fixture.network_error = True
        code, out, err, files = self.run_embedded(fixture)
        self.assertEqual(code, 1); self.assertEqual(out, ""); self.assertEqual(files, {})
        self.assertNotIn("must-not-be-printed", err)
        self.assertFalse(any("github" in url for url in fixture.calls))

    def test_bad_tag_fails_before_network(self):
        for tag in ("../v1.0.0", "v1.2.3-rc.1", "v1.2.3?token=x"):
            fixture = Fixture(); code, _, _, files = self.run_embedded(fixture, tag)
            self.assertEqual(code, 1); self.assertEqual(fixture.calls, []); self.assertEqual(files, {})

    def test_full_two_source_install_and_corruption_preserves_previous_program(self):
        sh = shutil.which("sh")
        if not sh:
            bundled = Path(r"C:/Users/hjw/.cache/codex-runtimes/codex-primary-runtime/dependencies/native/git/usr/bin/sh.exe")
            sh = str(bundled) if bundled.exists() else None
        if not sh: self.skipTest("POSIX shell unavailable")
        for source, corrupt in itertools.product(("github", "gitee"), (False, True)):
            with self.subTest(source=source, corrupt=corrupt), tempfile.TemporaryDirectory() as temp:
                root = Path(temp); stubs = root / "stubs"; stubs.mkdir()
                destination = root / "install"; destination.mkdir()
                original = b"previous local test program"
                (destination / "pipewright").write_bytes(original)
                program = b'#!/bin/sh\nprintf "%s\\n" "pipewright fixture v2.1.2"\n'
                archive = root / "pipewright_2.1.2_linux_amd64.tar.gz"
                with tarfile.open(archive, "w:gz") as tf:
                    member = tarfile.TarInfo("pipewright"); member.mode = 0o755; member.size = len(program)
                    tf.addfile(member, io.BytesIO(program))
                checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
                (root / "checksums.txt").write_text(checksum + "  " + archive.name + "\n", encoding="utf-8")
                helper = root / "download_fixture.py"
                helper.write_text("""import os,sys,pathlib,hashlib,tarfile
if sys.argv[1]=='sha256sum':
 path=pathlib.Path(sys.argv[2]);print(hashlib.sha256(path.read_bytes()).hexdigest()+'  '+str(path));raise SystemExit(0)
if sys.argv[1]=='tar':
 args=sys.argv[2:];destination=pathlib.Path(args[args.index('-C')+1])
 with tarfile.open(args[args.index('-xzf')+1],'r:gz') as tf:
  raw=tf.extractfile('pipewright').read();(destination/'pipewright').write_bytes(raw)
 raise SystemExit(0)
args=sys.argv[2:];url=args[-1]
allowed='https://github.com/official/pipewright/releases/download/v2.1.2/'
if not url.startswith(allowed): raise SystemExit(9)
name=url[len(allowed):]
if name not in ('pipewright_2.1.2_linux_amd64.tar.gz','checksums.txt'): raise SystemExit(10)
raw=(pathlib.Path(os.environ['TEST_FIXTURE'])/name).read_bytes()
if os.environ['TEST_CORRUPT']=='1' and name.endswith('.tar.gz'): raw=b'corrupt download'
out=pathlib.Path(args[args.index('-o')+1]);out.write_bytes(raw)
""", encoding="utf-8")
                fixture_names = {f"pipewright_2.1.2_{platform}_{cpu}.{'zip' if platform == 'windows' else 'tar.gz'}"
                                 for platform in ("darwin", "linux", "windows") for cpu in ("amd64", "arm64")}
                fixture_sums = {}
                for name in fixture_names:
                    asset = root / name
                    if not asset.exists(): asset.write_bytes(("other platform fixture:" + name).encode())
                    fixture_sums[name] = hashlib.sha256(asset.read_bytes()).hexdigest()
                (root / "checksums.txt").write_text("".join(f"{fixture_sums[name]}  {name}\n" for name in sorted(fixture_names)), encoding="utf-8")
                (root / "pipewright-release.json").write_text(json.dumps({"schemaVersion": 1, "tag": TAG, "commit": COMMIT,
                    "checksumsSha256": hashlib.sha256((root / "checksums.txt").read_bytes()).hexdigest(), "assets": fixture_sums}), encoding="utf-8")
                (root / "release-metadata.json").write_text(json.dumps({"tag_name": TAG, "target_commitish": COMMIT,
                    "prerelease": False, "assets": [{"name": n} for n in fixture_names.union({"checksums.txt", "pipewright-release.json"})]}), encoding="utf-8")
                embedded_runner = root / "embedded_fixture.py"
                embedded_runner.write_text("""import os,sys,io,pathlib
from unittest.mock import patch
root=pathlib.Path(os.environ['TEST_FIXTURE'])
class Response(io.BytesIO): status=200
class Opener:
 def open(self,request,timeout):
  url=request.full_url
  if not url.startswith('https://gitee.com/'): raise AssertionError('unexpected host')
  name='release-metadata.json' if '/api/v5/' in url else url.rsplit('/',1)[-1]
  raw=(root/name).read_bytes()
  if os.environ['TEST_CORRUPT']=='1' and name.endswith('.tar.gz'): raw=b'corrupted mirror package'
  return Response(raw)
code=sys.stdin.buffer.read().decode('utf-8');sys.argv=sys.argv[1:]
with patch('urllib.request.build_opener',return_value=Opener()): exec(compile(code,'gitee-installer-fixture','exec'),{'__name__':'__main__'})
""", encoding="utf-8")
                commands = {"curl": '#!/bin/sh\nexec "$TEST_PYTHON" "$TEST_HELPER" curl "$@"\n',
                            "python3": '#!/bin/sh\nexec "$TEST_PYTHON" "$TEST_EMBEDDED_RUNNER" "$@"\n',
                            "sha256sum": '#!/bin/sh\nexec "$TEST_PYTHON" "$TEST_HELPER" sha256sum "$@"\n',
                            "tar": '#!/bin/sh\nexec "$TEST_PYTHON" "$TEST_HELPER" tar "$@"\n',
                            "chmod": '#!/bin/sh\nexit 0\n',
                            "uname": '#!/bin/sh\ncase "$1" in -s) echo Linux ;; -m) echo x86_64 ;; *) exit 1 ;; esac\n',
                            "docker": '#!/bin/sh\nexit 0\n', "systemctl": '#!/bin/sh\nexit 0\n'}
                for name, content in commands.items():
                    (stubs / name).write_text(content, encoding="utf-8", newline="\n")
                    (stubs / name).chmod(0o755)
                env = {**os.environ, "PATH": os.pathsep.join([str(stubs), str(Path(sh).parent), os.environ.get("PATH", "")]),
                    "PYTHONIOENCODING": "utf-8", "SOURCE": source, "PIPEWRIGHT_RELEASE_REPO": "official/pipewright", "PIPEWRIGHT_GITEE_RELEASE_REPO": "official/pipewright", "VERSION": TAG,
                    "SETUP_SERVICE": "0", "INSTALL_DOCKER": "0", "INSTALL_DIR": destination.as_posix(),
                    "TEST_PYTHON": Path(sys.executable).as_posix(), "TEST_HELPER": helper.as_posix(),
                    "TEST_FIXTURE": root.as_posix(), "TEST_EMBEDDED_RUNNER": embedded_runner.as_posix(), "TEST_CORRUPT": "1" if corrupt else "0"}
                result = subprocess.run([sh, str(SCRIPT)], env=env, capture_output=True, text=True, encoding="utf-8")
                if corrupt:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual((destination / "pipewright").read_bytes(), original)
                else:
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual((destination / "pipewright").read_bytes(), program)

    def test_shell_repository_preflight_preserves_legal_fork_names(self):
        sh = shutil.which("sh")
        if not sh:
            candidate = Path(r"C:/Users/hjw/.cache/codex-runtimes/codex-primary-runtime/dependencies/native/git/usr/bin/sh.exe")
            sh = str(candidate) if candidate.exists() else None
        if not sh: self.skipTest("POSIX shell unavailable")
        prefix = SOURCE.split('BIN="pipewright"', 1)[0] + "\nprintf accepted\n"
        for repo in ("owner/.pipewright", "owner/-pipewright", "owner/pipewright..mirror", "owner/.", "owner/.."):
            env = {**os.environ, "PATH": os.pathsep.join([str(Path(sh).parent), os.environ.get("PATH", "")]), "SOURCE": "github", "PIPEWRIGHT_RELEASE_REPO": repo}
            result = subprocess.run([sh, "-c", prefix], env=env, capture_output=True)
            self.assertEqual(result.returncode == 0, repo not in ("owner/.", "owner/.."), (repo, result.stderr))
            if result.returncode == 0: self.assertEqual(result.stdout, b"accepted")

    def test_shell_syntax_and_bad_source_fail_before_install(self):
        sh = shutil.which("sh")
        if not sh:
            bundled = Path(r"C:/Users/hjw/.cache/codex-runtimes/codex-primary-runtime/dependencies/native/git/usr/bin/sh.exe")
            sh = str(bundled) if bundled.exists() else None
        if not sh: self.skipTest("POSIX shell unavailable")
        checked = subprocess.run([sh, "-n", str(SCRIPT)], capture_output=True, text=True)
        self.assertEqual(checked.returncode, 0, checked.stderr)
        for source, repo in (("evil", ""), ("gitee", ""), ("github", "../bad")):
            env = {**os.environ, "SOURCE": source, "PIPEWRIGHT_GITEE_RELEASE_REPO": repo, "PIPEWRIGHT_RELEASE_REPO": repo}
            result = subprocess.run([sh, str(SCRIPT)], env=env, capture_output=True, text=True, encoding="utf-8")
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("https://", result.stderr)


if __name__ == "__main__": unittest.main()

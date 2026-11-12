#!/usr/bin/env python3
"""Copy original stable GitHub artifacts to an explicitly configured Gitee mirror.

Only the selected already-published GitHub tag is copied, without force or branch updates.
No repositories, credentials or retention policies are created here.
A failed or uncertain write stops; the next explicit run first reads remote state.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import socket
import ssl
import time
import sys
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid

MAX_FILE = 100 * 1024 * 1024
MAX_JSON = 1024 * 1024
API_TIMEOUT_SECONDS = 30
ARTIFACT_TIMEOUT_SECONDS = 180
MARKER = "pipewright-release.json"
TAG = re.compile(r"^v?[0-9]+\.[0-9]+\.[0-9]+$")
SHA = re.compile(r"^[0-9a-f]{64}$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")
REPO = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
DOWNLOAD_HOSTS = {"gitee.com", "foruda.gitee.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"}


class SyncError(Exception):
    pass


def unique_object(pairs):
    obj = {}
    for key, value in pairs:
        if key in obj:
            raise SyncError("duplicate JSON property")
        obj[key] = value
    return obj


def json_bytes(value):
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def parse_json(raw):
    try:
        return json.loads(raw, object_pairs_hook=unique_object)
    except (ValueError, UnicodeError) as exc:
        raise SyncError("invalid JSON response") from exc


def valid_repository(repo):
    return isinstance(repo, str) and REPO.fullmatch(repo) is not None and all(segment not in (".", "..") for segment in repo.split("/"))


def names(tag):
    version = tag.removeprefix("v")
    return sorted(f"pipewright_{version}_{osname}_{arch}.{'zip' if osname == 'windows' else 'tar.gz'}"
                  for osname in ("darwin", "linux", "windows") for arch in ("amd64", "arm64"))


def checksum_entries(raw, expected):
    try:
        lines = raw.decode("utf-8").splitlines()
    except UnicodeError as exc:
        raise SyncError("invalid checksum encoding") from exc
    result = {}
    for line in lines:
        if not line.strip():
            continue
        fields = line.split()
        if len(fields) != 2 or not SHA.fullmatch(fields[0]):
            raise SyncError("invalid checksum entry")
        name = fields[1].removeprefix("*")
        if name not in expected or name in result:
            raise SyncError("unexpected or duplicate checksum asset")
        result[name] = fields[0]
    if set(result) != set(expected):
        raise SyncError("all six original archive checksums are required")
    return result


class Redirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        parsed = urllib.parse.urlsplit(newurl)
        origin = urllib.parse.urlsplit(req.full_url)
        if (req.get_method() != "GET" or parsed.scheme != "https" or parsed.username
                or parsed.password or parsed.port not in (None, 443)
                or parsed.hostname not in DOWNLOAD_HOSTS or parsed.fragment):
            raise SyncError("unsafe release redirect")
        if origin.hostname == "api.github.com" or origin.path.startswith("/api/v5/"):
            raise SyncError("unexpected API redirect")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def endpoint_category(url):
    """Return only fixed labels; never echo URL parts, queries or credentials."""
    parsed = urllib.parse.urlsplit(url)
    host, path = parsed.hostname, parsed.path
    if host == "api.github.com":
        return "github_git_api" if "/git/" in path else "github_release_api"
    if host == "gitee.com" and path.startswith("/api/v5/"):
        if path == "/api/v5/user":
            return "gitee_identity_api"
        return "gitee_attachment_api" if "/attach_files" in path else "gitee_release_api"
    if host == "github.com" and "/releases/download/" in path:
        return "github_artifact"
    if host == "gitee.com" and "/releases/download/" in path:
        return "gitee_artifact"
    return "other"


def transport_error_kind(exc):
    reason = exc.reason if isinstance(exc, urllib.error.URLError) else exc
    if isinstance(reason, TimeoutError):
        return "timeout"
    if isinstance(reason, ssl.SSLError):
        return "tls"
    if isinstance(reason, socket.gaierror):
        return "dns"
    if isinstance(reason, http.client.HTTPException):
        return "protocol"
    return "network"


class Client:
    def __init__(self):
        self.opener = urllib.request.build_opener(Redirects())

    def read(self, url, limit=MAX_JSON, method="GET", data=None, headers=None, missing=False):
        headers = {"User-Agent": "pipewright-release-mirror/1", "Accept-Encoding": "identity", **(headers or {})}
        req = urllib.request.Request(url, data=data, method=method, headers=headers)
        endpoint = endpoint_category(url)
        safe_method = method if method in ("GET", "POST", "PATCH") else "other"
        write = method != "GET"
        large = ((method == "GET" and endpoint in ("github_artifact", "gitee_artifact"))
                 or (method == "POST" and endpoint == "gitee_attachment_api"))
        # Socket operation timeouts, not a whole-request wall-clock deadline.
        # The workflow's existing 30-minute deadline bounds the complete job.
        timeout = ARTIFACT_TIMEOUT_SECONDS if large else API_TIMEOUT_SECONDS
        started = time.monotonic()
        phase = "open"

        def report(event, *, error=None, status=None):
            record = {"event": event, "endpoint": endpoint, "method": safe_method,
                      "phase": phase, "elapsed_ms": round((time.monotonic() - started) * 1000),
                      "timeout_seconds": timeout}
            if error:
                record["error"] = error
                record["outcome"] = "uncertain" if write else "read_failed"
            if status is not None:
                record["http_status"] = status
            print(json.dumps(record, sort_keys=True), file=sys.stderr, flush=True)

        report("request_started")
        try:
            with self.opener.open(req, timeout=timeout) as response:
                phase = "read_response"
                if response.status not in (200, 201):
                    raise SyncError("unexpected release response status")
                result = response.read(limit + 1)
                if len(result) > limit:
                    raise SyncError("release response too large")
                report("request_completed")
                return result
        except urllib.error.HTTPError as exc:
            status = exc.code
            exc.close()
            if missing and method == "GET" and status == 404:
                report("request_missing", status=status)
                return None
            report("request_failed", error="http", status=status)
            outcome = "write outcome uncertain; inspect state before explicit rerun" if write else "read failed"
            raise SyncError(f"{safe_method} {endpoint} {phase}: HTTP {status}; {outcome}; no automatic retry") from None
        except (urllib.error.URLError, TimeoutError, OSError, http.client.HTTPException) as exc:
            error = transport_error_kind(exc)
            report("request_failed", error=error)
            outcome = "write outcome uncertain; inspect state before explicit rerun" if write else "read failed"
            # Never render exception text: it may contain tokens, signed URLs or response data.
            raise SyncError(f"{safe_method} {endpoint} {phase}: {error}; {outcome}; no automatic retry") from None
        except SyncError:
            report("request_failed", error="validation")
            outcome = "write outcome uncertain; inspect state before explicit rerun" if write else "read failed"
            raise SyncError(f"{safe_method} {endpoint} {phase}: response validation failed; {outcome}") from None

    def get_json(self, url, missing=False):
        raw = self.read(url, missing=missing)
        return None if raw is None else parse_json(raw)

    def authenticated_login(self, token):
        if not token:
            raise SyncError("GITEE_RELEASE_TOKEN is required")
        profile = parse_json(self.read("https://gitee.com/api/v5/user", headers={"Authorization": "Bearer " + token}))
        login = profile.get("login", "") if isinstance(profile, dict) else ""
        if not isinstance(login, str) or not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", login) or ".." in login:
            raise SyncError("authenticated Gitee account identity is invalid")
        return login

    def form(self, url, fields, token, method="POST", file=None):
        if not token:
            raise SyncError("GITEE_RELEASE_TOKEN is required")
        if file is None:
            data = urllib.parse.urlencode({"access_token": token, **fields}).encode()
            content_type = "application/x-www-form-urlencoded"
        else:
            name, payload = file
            boundary = "pipewright-" + uuid.uuid4().hex
            chunks = []
            for key, value in {"access_token": token, **fields}.items():
                chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
            chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode())
            chunks += [payload, f"\r\n--{boundary}--\r\n".encode()]
            data = b"".join(chunks)
            content_type = "multipart/form-data; boundary=" + boundary
        return parse_json(self.read(url, method=method, data=data, headers={"Content-Type": content_type}))


def release_assets(release):
    result = {}
    if not isinstance(release.get("assets"), list):
        raise SyncError("missing release assets")
    for asset in release["assets"]:
        if not isinstance(asset, dict) or not isinstance(asset.get("name"), str):
            raise SyncError("invalid release asset metadata")
        name = asset["name"]
        if name in result:
            raise SyncError("duplicate release asset metadata")
        result[name] = asset
    return result


def github_commit(client, repo, tag):
    ref = client.get_json(f"https://api.github.com/repos/{repo}/git/ref/tags/{tag}")
    obj = ref.get("object", {})
    for _ in range(5):
        if obj.get("type") == "commit" and COMMIT.fullmatch(str(obj.get("sha", ""))):
            return obj["sha"]
        if obj.get("type") != "tag" or not COMMIT.fullmatch(str(obj.get("sha", ""))):
            break
        obj = client.get_json(f"https://api.github.com/repos/{repo}/git/tags/{obj['sha']}").get("object", {})
    raise SyncError("GitHub tag does not resolve to a full commit")


class GitCommand:
    """Execute isolated HTTPS Git commands; secrets exist only in child memory."""
    def __init__(self, runner=None):
        self.runner = runner or subprocess.run

    def run(self, args, *, cwd, token="", username="", allowed=(0,)):
        # No inherited tracing, config parameters, askpass, credential helpers or
        # source tokens may reach git. This also avoids user URL rewrites/hooks.
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("GIT_") and key not in
               {"GITEE_RELEASE_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", "SSH_ASKPASS", "GCM_TRACE", "GCM_INTERACTIVE"}}
        env.update({"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.devnull,
                    "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CEILING_DIRECTORIES": str(Path(cwd).resolve()),
                    "GIT_TERMINAL_PROMPT": "0",
                    "GIT_ASKPASS": "", "SSH_ASKPASS": "", "GCM_INTERACTIVE": "Never"})
        settings = [("credential.helper", ""), ("core.hooksPath", os.devnull),
                    ("http.followRedirects", "false"), ("http.sslVerify", "true"),
                    ("http.lowSpeedLimit", "1024"), ("http.lowSpeedTime", "30"),
                    ("protocol.allow", "never"), ("protocol.https.allow", "always"),
                    ("transfer.fsckObjects", "true"), ("http.extraHeader", "")]
        if token:
            if not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", username) or ".." in username:
                raise SyncError("authenticated Gitee account identity is required for Git push")
            encoded = base64.b64encode((username + ":" + token).encode()).decode("ascii")
            settings.append(("http.https://gitee.com/.extraHeader", "Authorization: Basic " + encoded))
        env["GIT_CONFIG_COUNT"] = str(len(settings))
        for index, (key, value) in enumerate(settings):
            env[f"GIT_CONFIG_KEY_{index}"] = key
            env[f"GIT_CONFIG_VALUE_{index}"] = value
        try:
            result = self.runner(["git", *args], cwd=cwd, env=env, shell=False,
                                 capture_output=True, check=False, timeout=300)
        except (OSError, subprocess.SubprocessError):
            raise SyncError("Git transfer failed or outcome uncertain; inspect the remote tag before explicit rerun") from None
        if result.returncode not in allowed:
            # Raw git output can echo remote errors and credential material.
            raise SyncError("Git transfer rejected or outcome uncertain; no automatic push retry")
        return result


def remote_tag_commit(git, *, repo, tag, cwd):
    ref = "refs/tags/" + tag
    result = git.run(["ls-remote", "--tags", "--exit-code", "https://gitee.com/" + repo + ".git",
                      ref, ref + "^{}"], cwd=cwd, allowed=(0, 2))
    if result.returncode == 2:
        if result.stdout.strip():
            raise SyncError("unexpected missing-tag Git response")
        return None
    try:
        lines = result.stdout.decode("ascii").splitlines()
    except UnicodeError:
        raise SyncError("invalid remote Git identity response") from None
    values = {}
    for line in lines:
        fields = line.split()
        if (len(fields) != 2 or not COMMIT.fullmatch(fields[0])
                or fields[1] not in (ref, ref + "^{}") or fields[1] in values):
            raise SyncError("invalid or duplicate remote tag identity")
        values[fields[1]] = fields[0]
    if ref not in values:
        raise SyncError("missing remote tag identity")
    return values.get(ref + "^{}", values[ref])


def ensure_gitee_tag(git, *, github_repo, gitee_repo, tag, commit, token, username_lookup=None):
    if (not TAG.fullmatch(tag) or not COMMIT.fullmatch(commit)
            or not valid_repository(github_repo) or not valid_repository(gitee_repo)):
        raise SyncError("invalid selected release Git identity")
    with tempfile.TemporaryDirectory(prefix="pipewright-release-tag-") as temporary:
        cwd = Path(temporary)
        existing = remote_tag_commit(git, repo=gitee_repo, tag=tag, cwd=cwd)
        if existing is not None:
            if existing != commit:
                raise SyncError("Gitee tag commit differs; existing tags are never replaced")
            return "already_present"
        if not token:
            raise SyncError("GITEE_RELEASE_TOKEN is required to copy the selected Git tag")
        if username_lookup is None:
            raise SyncError("Gitee account lookup is required before Git push")
        username = username_lookup(token)
        if not isinstance(username, str) or not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", username) or ".." in username:
            raise SyncError("authenticated Gitee account identity is invalid")
        ref = "refs/tags/" + tag
        bare = cwd / "objects.git"
        git.run(["init", "--bare", "--quiet", str(bare)], cwd=cwd)
        git.run(["-C", str(bare), "fetch", "--quiet", "--no-tags", "--no-recurse-submodules",
                 "https://github.com/" + github_repo + ".git", ref + ":" + ref], cwd=cwd)
        resolved = git.run(["-C", str(bare), "rev-parse", "--verify", ref + "^{commit}"], cwd=cwd)
        try:
            fetched_commit = resolved.stdout.decode("ascii").strip()
        except UnicodeError:
            raise SyncError("invalid fetched Git commit") from None
        if fetched_commit != commit:
            raise SyncError("fetched GitHub tag moved or differs from the published release")
        # Copy only this exact tag. No --force, mirror, all-tags, branch refspec,
        # persistent remote, credential helper, shallow object graph or hooks.
        git.run(["-C", str(bare), "push", "--porcelain", "--no-verify",
                 "https://gitee.com/" + gitee_repo + ".git", ref + ":" + ref], cwd=cwd, token=token, username=username)
        if remote_tag_commit(git, repo=gitee_repo, tag=tag, cwd=cwd) != commit:
            raise SyncError("Git tag push final identity is uncertain; inspect before explicit rerun")
        return "pushed"


def attachments(client, api, release_id):
    result = {}
    for page in range(1, 101):
        items = client.get_json(f"{api}/releases/{release_id}/attach_files?page={page}&per_page=100")
        if not isinstance(items, list):
            raise SyncError("invalid Gitee attachment listing")
        for item in items:
            name = item.get("name")
            if not isinstance(name, str) or name in result:
                raise SyncError("invalid or duplicate Gitee attachment name")
            result[name] = item
        if len(items) < 100:
            return result
    raise SyncError("Gitee attachment list exceeded safety bound")


def synchronize(client, *, github_repo, gitee_repo, tag, directory, token, expected_commit=""):
    if (not TAG.fullmatch(tag) or not valid_repository(github_repo) or not valid_repository(gitee_repo)):
        raise SyncError("stable vX.Y.Z tag and explicit owner/repository paths are required")
    if expected_commit and not COMMIT.fullmatch(expected_commit):
        raise SyncError("expected commit must be the full SHA")
    release = client.get_json(f"https://api.github.com/repos/{github_repo}/releases/tags/{tag}")
    if release.get("tag_name") != tag or release.get("draft") is not False or release.get("prerelease") is not False:
        raise SyncError("only an existing stable published GitHub Release can be mirrored")
    commit = github_commit(client, github_repo, tag)
    if expected_commit and expected_commit != commit:
        raise SyncError("upstream run commit differs from release tag")
    expected = names(tag)
    github_assets = release_assets(release)
    required = [*expected, "checksums.txt"]
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    payloads = {}
    for name in required:
        info = github_assets.get(name)
        digest = info.get("digest", "") if info else ""
        if not digest.startswith("sha256:") or not SHA.fullmatch(digest[7:]):
            raise SyncError("original GitHub asset SHA256 metadata is required")
        path = directory / name
        if path.is_symlink():
            raise SyncError("local artifact must not be a symlink")
        if path.exists():
            if not path.is_file() or path.stat().st_size > MAX_FILE:
                raise SyncError("invalid local original artifact")
            raw = path.read_bytes()
        else:
            raw = client.read(f"https://github.com/{github_repo}/releases/download/{tag}/{name}", MAX_FILE)
            path.write_bytes(raw)
        if hashlib.sha256(raw).hexdigest() != digest[7:] or len(raw) != info.get("size"):
            raise SyncError("local original artifact differs from GitHub Release metadata")
        payloads[name] = raw
    checksums = checksum_entries(payloads["checksums.txt"], expected)
    for name in expected:
        if hashlib.sha256(payloads[name]).hexdigest() != checksums[name]:
            raise SyncError("original archive does not match original checksums.txt")
    marker = {"schemaVersion": 1, "tag": tag, "commit": commit,
              "checksumsSha256": hashlib.sha256(payloads["checksums.txt"]).hexdigest(), "assets": checksums}
    marker_bytes = json_bytes(marker)
    payloads[MARKER] = marker_bytes
    api = f"https://gitee.com/api/v5/repos/{gitee_repo}"
    base = f"https://gitee.com/{gitee_repo}/releases/download/{tag}"
    ensure_gitee_tag(GitCommand(), github_repo=github_repo, gitee_repo=gitee_repo, tag=tag, commit=commit, token=token, username_lookup=client.authenticated_login)
    mirror = client.get_json(f"{api}/releases/tags/{tag}", missing=True)
    if mirror is None:
        mirror = client.form(f"{api}/releases", {"tag_name": tag, "name": f"Pipewright {tag}",
            "body": f"Original release: https://github.com/{github_repo}/releases/tag/{tag}",
            "target_commitish": commit, "prerelease": "true"}, token)
    if (mirror.get("tag_name") != tag or mirror.get("target_commitish") != commit
            or type(mirror.get("id")) is not int or mirror["id"] <= 0
            or type(mirror.get("prerelease")) is not bool):
        raise SyncError("Gitee release identity does not match original tag and commit")
    release_id = mirror["id"]
    existing = attachments(client, api, release_id)
    if set(existing) - set(payloads):
        raise SyncError("unexpected mirror attachments; refusing to mutate release")
    if not mirror["prerelease"] and set(existing) != set(payloads):
        raise SyncError("published mirror is incomplete; refusing to mutate an already published release")
    # Preflight every existing object before the first upload. Never delete/replace.
    for name in existing:
        if client.read(f"{base}/{name}", MAX_FILE) != payloads[name]:
            raise SyncError("existing mirror artifact differs; no overwrite is permitted")
    changed = False
    for name in [*required, MARKER]:
        if name in existing:
            continue
        client.form(f"{api}/releases/{release_id}/attach_files", {}, token, file=(name, payloads[name]))
        if client.read(f"{base}/{name}", MAX_FILE) != payloads[name]:
            raise SyncError("anonymous attachment readback did not match original bytes")
        changed = True
    if mirror["prerelease"]:
        final = client.form(f"{api}/releases/{release_id}", {"tag_name": tag,
            "name": mirror.get("name") or f"Pipewright {tag}", "body": mirror.get("body") or f"Pipewright {tag}",
            "prerelease": "false"}, token, method="PATCH")
        if final.get("prerelease") is not False or final.get("tag_name") != tag or final.get("target_commitish") != commit:
            raise SyncError("mirror publication outcome uncertain; inspect before explicit rerun")
        changed = True
    final = client.get_json(f"{api}/releases/tags/{tag}")
    if final.get("prerelease") is not False or final.get("target_commitish") != commit:
        raise SyncError("mirror release final readback failed")
    if client.read(f"{base}/{MARKER}") != marker_bytes:
        raise SyncError("mirror completion marker final readback failed")
    return {"tag": tag, "commit": commit, "giteeRepository": gitee_repo,
            "result": "published" if changed else "already_identical", "archives": len(expected)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--github-repo", default="huangchengsir/pipewright")
    parser.add_argument("--gitee-repo", default=os.environ.get("GITEE_RELEASE_REPO", ""))
    parser.add_argument("--expected-commit", default="")
    parser.add_argument("--dist-dir", type=Path, required=True)
    args = parser.parse_args()
    try:
        result = synchronize(Client(), github_repo=args.github_repo, gitee_repo=args.gitee_repo,
            tag=args.tag, directory=args.dist_dir, token=os.environ.get("GITEE_RELEASE_TOKEN", ""),
            expected_commit=args.expected_commit)
        print(json.dumps(result, sort_keys=True))
    except SyncError as exc:
        print("Gitee mirror synchronization stopped: " + str(exc), file=sys.stderr)
        return 1
    except Exception:
        # Generic boundary: no HTTP bodies, signed CDN links, token or raw exception.
        print("Gitee mirror synchronization stopped. Verify tag, original artifacts, mirror state and configured permissions; no automatic write retry.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

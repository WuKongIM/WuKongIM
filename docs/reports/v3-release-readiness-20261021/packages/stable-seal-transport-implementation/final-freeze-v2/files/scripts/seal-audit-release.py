#!/usr/bin/env python3
"""Fill and seal one exact numeric native-package audit Release.

The writer is intentionally narrow: it may add only the two canonical assets
to an exact draft, publish that draft once, or re-verify an already immutable
Release.  It never discovers Releases by tag and never deletes, replaces, or
renames a remote asset.
"""

from __future__ import annotations

import argparse
import hashlib
import http.client
import importlib.util
import json
import os
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
RESOLVER_PATH = ROOT / "scripts" / "resolve-audit-release.py"
RESOLVER_SPEC = importlib.util.spec_from_file_location(
    "wukongim_resolve_audit_release", RESOLVER_PATH
)
if RESOLVER_SPEC is None or RESOLVER_SPEC.loader is None:
    raise RuntimeError("cannot load audit Release resolver")
resolver = importlib.util.module_from_spec(RESOLVER_SPEC)
RESOLVER_SPEC.loader.exec_module(resolver)


SEAL_SCHEMA = "wukongim/package-audit-release-seal/v1"
READ_CHUNK = 1024 * 1024


class SealError(RuntimeError):
    """The exact audit Release could not be filled or sealed safely."""


def _regular_artifact(path: Path, expected_name: str, maximum: int) -> dict[str, Any]:
    path = Path(path)
    if path.name != expected_name:
        raise SealError(f"local audit asset must be named {expected_name}")
    try:
        before = path.lstat()
    except OSError as error:
        raise SealError(f"cannot inspect local audit asset {expected_name}") from error
    if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1:
        raise SealError(f"local audit asset must be a single-link regular file: {expected_name}")
    if before.st_size <= 0 or before.st_size > maximum:
        raise SealError(f"local audit asset has an invalid size: {expected_name}")
    flags = os.O_RDONLY
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        raise SealError(f"cannot safely open local audit asset {expected_name}") from error
    digest = hashlib.sha256()
    total = 0
    try:
        opened = os.fstat(descriptor)
        if (
            not stat.S_ISREG(opened.st_mode)
            or opened.st_nlink != 1
            or (opened.st_dev, opened.st_ino, opened.st_size)
            != (before.st_dev, before.st_ino, before.st_size)
        ):
            raise SealError(f"local audit asset changed while opening: {expected_name}")
        while True:
            chunk = os.read(descriptor, READ_CHUNK)
            if not chunk:
                break
            digest.update(chunk)
            total += len(chunk)
        after = os.fstat(descriptor)
        if (
            total != opened.st_size
            or (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
            != (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns)
        ):
            raise SealError(f"local audit asset changed while reading: {expected_name}")
    finally:
        os.close(descriptor)
    return {
        "name": expected_name,
        "path": path,
        "size": total,
        "sha256": digest.hexdigest(),
        "device": before.st_dev,
        "inode": before.st_ino,
        "mtime_ns": before.st_mtime_ns,
    }


class GitHubWriter(resolver.GitHubReader):
    """Resolver transport plus the two exact write primitives needed here."""

    def _json_write(self, method: str, url: str, body: dict[str, Any]) -> dict[str, Any]:
        parsed = urllib.parse.urlsplit(url)
        api = urllib.parse.urlsplit(self.api_base_url)
        if resolver._origin(url) != resolver._origin(self.api_base_url):
            raise SealError("GitHub JSON write URL must use the configured API origin")
        if parsed.scheme != api.scheme or not parsed.netloc:
            raise SealError("GitHub JSON write URL is invalid")
        data = json.dumps(body, sort_keys=True, separators=(",", ":")).encode()
        request = urllib.request.Request(
            url,
            data=data,
            headers={
                **self._headers(authorize=True, accept="application/vnd.github+json"),
                "Content-Type": "application/json",
            },
            method=method,
        )
        try:
            with self._no_redirect.open(request, timeout=30) as response:
                raw = response.read(resolver.MAX_JSON_BYTES + 1)
        except (OSError, urllib.error.HTTPError) as error:
            raise SealError(f"GitHub {method} failed: {error}") from error
        if len(raw) > resolver.MAX_JSON_BYTES:
            raise SealError("GitHub JSON write response is too large")
        try:
            return resolver._require_object(json.loads(raw), "GitHub write response")
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise SealError("GitHub write response is not valid JSON") from error

    def patch_release(self, release_id: int, body: dict[str, Any]) -> dict[str, Any]:
        return self._json_write(
            "PATCH",
            f"{self.api_base_url}/repos/{resolver.AUDIT_REPOSITORY}/releases/{release_id}",
            body,
        )

    def upload_asset(
        self, upload_template: str, release_id: int, artifact: dict[str, Any]
    ) -> dict[str, Any]:
        template_suffix = "{?name,label}"
        if not isinstance(upload_template, str) or not upload_template.endswith(
            template_suffix
        ):
            raise SealError("audit Release upload_url has an invalid template")
        base = upload_template[: -len(template_suffix)]
        parsed = urllib.parse.urlsplit(base)
        api = urllib.parse.urlsplit(self.api_base_url)
        if not parsed.netloc or parsed.query or parsed.fragment:
            raise SealError("audit Release upload_url is invalid")
        if not (
            (parsed.scheme == "https" and api.scheme == "https")
            or (
                parsed.scheme == "http"
                and resolver._is_loopback_host(parsed.hostname or "")
                and api.scheme == "http"
            )
        ):
            raise SealError("audit Release upload_url must use HTTPS or loopback HTTP")
        if api.hostname == "api.github.com":
            if parsed.hostname != "uploads.github.com" or parsed.port is not None:
                raise SealError("GitHub.com audit uploads must use uploads.github.com")
        elif resolver._is_loopback_host(api.hostname or ""):
            if resolver._origin(base) != resolver._origin(self.api_base_url):
                raise SealError("loopback audit uploads must use the API origin")
        else:
            raise SealError("unsupported audit Release upload origin")
        expected_path = (
            f"/repos/{resolver.AUDIT_REPOSITORY}/releases/{release_id}/assets"
        )
        if parsed.path != expected_path:
            raise SealError("audit Release upload_url is not bound to the numeric Release id")
        url = f"{base}?{urllib.parse.urlencode({'name': artifact['name']})}"
        headers = {
            **self._headers(authorize=True, accept="application/vnd.github+json"),
            "Content-Type": "application/octet-stream",
            "Content-Length": str(artifact["size"]),
        }
        connection_type = (
            http.client.HTTPSConnection
            if parsed.scheme == "https"
            else http.client.HTTPConnection
        )
        connection = connection_type(parsed.hostname, parsed.port, timeout=120)
        try:
            request_target = urllib.parse.urlunsplit(
                ("", "", parsed.path, urllib.parse.urlsplit(url).query, "")
            )
            connection.putrequest("POST", request_target)
            for key, value in headers.items():
                connection.putheader(key, value)
            connection.endheaders()
            flags = os.O_RDONLY
            if hasattr(os, "O_NOFOLLOW"):
                flags |= os.O_NOFOLLOW
            descriptor = os.open(artifact["path"], flags)
            try:
                opened = os.fstat(descriptor)
                if (
                    not stat.S_ISREG(opened.st_mode)
                    or opened.st_nlink != 1
                    or (opened.st_dev, opened.st_ino, opened.st_size, opened.st_mtime_ns)
                    != (
                        artifact["device"],
                        artifact["inode"],
                        artifact["size"],
                        artifact["mtime_ns"],
                    )
                ):
                    raise SealError(
                        f"local audit asset changed before upload: {artifact['name']}"
                    )
                sent = 0
                while True:
                    chunk = os.read(descriptor, READ_CHUNK)
                    if not chunk:
                        break
                    connection.send(chunk)
                    sent += len(chunk)
                after = os.fstat(descriptor)
                if (
                    sent != artifact["size"]
                    or (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns)
                    != (
                        artifact["device"],
                        artifact["inode"],
                        artifact["size"],
                        artifact["mtime_ns"],
                    )
                ):
                    raise SealError(
                        f"local audit asset changed during upload: {artifact['name']}"
                    )
            finally:
                os.close(descriptor)
            response = connection.getresponse()
            if response.status < 200 or response.status >= 300:
                response.read(resolver.MAX_JSON_BYTES + 1)
                raise SealError(
                    f"audit asset upload failed with HTTP {response.status}: {artifact['name']}"
                )
            raw = response.read(resolver.MAX_JSON_BYTES + 1)
        except (OSError, http.client.HTTPException) as error:
            raise SealError(f"audit asset upload failed: {artifact['name']}") from error
        finally:
            connection.close()
        if len(raw) > resolver.MAX_JSON_BYTES:
            raise SealError("audit asset upload response is too large")
        try:
            return resolver._require_object(json.loads(raw), "audit asset upload response")
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise SealError("audit asset upload response is not valid JSON") from error


def _snapshot(
    writer: GitHubWriter, release_id: int, expected_control_sha: str,
    channel: str = "preview", candidate: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    raw = writer.api_json(
        f"repos/{resolver.AUDIT_REPOSITORY}/releases/{release_id}"
    )
    try:
        value = resolver._release_snapshot(raw, release_id, expected_control_sha, channel, candidate)
    except resolver.ResolutionError as error:
        raise SealError(str(error)) from error
    return raw, value


def _require_current_control_and_reserved_tag(
    writer: GitHubWriter, tag_name: str, expected_control_sha: str
) -> None:
    """Fence protected main and the create-only audit tag around every write."""

    branch = writer.api_json(
        f"repos/{resolver.AUDIT_REPOSITORY}/git/ref/heads/main"
    )
    target = resolver._require_object(branch.get("object"), "main branch target")
    if (
        branch.get("ref") != "refs/heads/main"
        or target.get("type") != "commit"
        or target.get("sha") != expected_control_sha
    ):
        raise SealError("protected main no longer points to the reviewed control commit")
    try:
        resolver._tag_snapshot(writer, tag_name, expected_control_sha)
    except resolver.ResolutionError as error:
        raise SealError(str(error)) from error


def _require_immutable_releases_enabled(reader: resolver.GitHubReader) -> None:
    """Fail closed unless repository-enforced immutable Releases are enabled."""
    try:
        policy = reader.api_json(
            f"repos/{resolver.AUDIT_REPOSITORY}/immutable-releases"
        )
    except resolver.ResolutionError as error:
        raise SealError(str(error)) from error
    if policy.get("enabled") is not True:
        raise SealError("repository immutable Releases must remain enabled")


def _assert_remote_matches(
    snapshot: dict[str, Any], local: dict[str, dict[str, Any]]
) -> None:
    for name, remote in snapshot["assets"].items():
        expected = local[name]
        if remote["size"] != expected["size"] or remote["sha256"] != expected["sha256"]:
            raise SealError(f"existing audit asset conflicts with local bytes: {name}")


def _download_and_compare(
    writer: GitHubWriter, remote: dict[str, Any], local: dict[str, Any]
) -> None:
    digest = hashlib.sha256()
    total = 0
    try:
        with writer.open_asset(remote["id"]) as response:
            while True:
                chunk = response.read(READ_CHUNK)
                if not chunk:
                    break
                total += len(chunk)
                if total > local["size"]:
                    raise SealError(f"remote audit asset exceeds local size: {remote['name']}")
                digest.update(chunk)
    except resolver.ResolutionError as error:
        raise SealError(str(error)) from error
    if total != local["size"] or digest.hexdigest() != local["sha256"]:
        raise SealError(f"remote audit asset bytes conflict with local bytes: {remote['name']}")


def seal_audit_release(
    *,
    release_id: int,
    expected_control_sha: str,
    archive_path: Path,
    receipt_path: Path,
    api_base_url: str,
    download_base_url: str,
    token: str | None,
    policy_token: str | None = None,
    max_polls: int = 20,
    poll_seconds: float = 2.0,
    channel: str = "preview", test_only: bool = False,
    candidate_plan: Path | None = None, control_root: Path | None = None,
    candidate_source_root: Path | None = None, apt_receipt: Path | None = None,
    rpm_receipt: Path | None = None, source_token: str | None = None,
) -> dict[str, Any]:
    """Add missing exact assets, publish once, and verify immutable bytes."""

    if not isinstance(release_id, int) or isinstance(release_id, bool) or release_id <= 0:
        raise SealError("release id must be a positive integer")
    if not isinstance(expected_control_sha, str) or not resolver.CONTROL_SHA_RE.fullmatch(
        expected_control_sha
    ):
        raise SealError("expected control SHA must be a lowercase 40-hex commit")
    if not isinstance(max_polls, int) or isinstance(max_polls, bool) or max_polls <= 0:
        raise SealError("max polls must be a positive integer")
    if poll_seconds < 0:
        raise SealError("poll seconds must not be negative")
    if not isinstance(token, str) or not token:
        raise SealError("a GitHub token is required to seal an audit Release")
    if policy_token is not None and (
        not isinstance(policy_token, str) or not policy_token
    ):
        raise SealError("policy token must be a non-empty string when provided")

    resolver.stable.network_mode(channel,test_only,api_base_url,download_base_url)
    candidate = None
    if channel == "stable":
        candidate=resolver.stable.candidate_identity(candidate_plan,receipt_path,test_only=test_only)
    elif any(item is not None for item in (candidate_plan,control_root,candidate_source_root,apt_receipt,rpm_receipt,source_token)):
        raise SealError("candidate inputs require explicit stable mode")
    archive_name, receipt_name = resolver.expected_asset_names(release_id,channel,candidate)
    local = {
        archive_name: _regular_artifact(
            archive_path, archive_name, resolver.MAX_ARCHIVE_BYTES
        ),
        receipt_name: _regular_artifact(
            receipt_path, receipt_name, resolver.MAX_RECEIPT_BYTES
        ),
    }
    writer = GitHubWriter(api_base_url, download_base_url, token, deny_redirects=test_only)
    policy_reader = resolver.GitHubReader(
        api_base_url, download_base_url, policy_token or token, deny_redirects=test_only
    )
    verified = None
    if channel == "stable":
        verified=resolver.stable.verify_seal_inputs(reader=writer,expected_control_sha=expected_control_sha,
            archive_path=archive_path,receipt_path=receipt_path,plan_path=candidate_plan,
            control_root=control_root,source_root=candidate_source_root,apt_receipt=apt_receipt,
            rpm_receipt=rpm_receipt,source_token=source_token,test_only=test_only)
    raw, current = _snapshot(writer, release_id, expected_control_sha, channel, candidate)
    _assert_remote_matches(current, local)
    initial_classification = current["classification"]
    _require_current_control_and_reserved_tag(
        writer, current["tag_name"], expected_control_sha
    )
    _require_immutable_releases_enabled(policy_reader)

    if current["draft"]:
        upload_template = raw.get("upload_url")
        for name in (archive_name, receipt_name):
            if name in current["assets"]:
                continue
            _require_current_control_and_reserved_tag(
                writer, current["tag_name"], expected_control_sha
            )
            _require_immutable_releases_enabled(policy_reader)
            writer.upload_asset(upload_template, release_id, local[name])
            _require_current_control_and_reserved_tag(
                writer, current["tag_name"], expected_control_sha
            )
            _require_immutable_releases_enabled(policy_reader)
            raw, current = _snapshot(writer, release_id, expected_control_sha, channel, candidate)
            _assert_remote_matches(current, local)
        if current["classification"] != resolver.COMPLETE_DRAFT:
            raise SealError("audit draft did not reach the exact complete asset state")
        for name in (archive_name, receipt_name):
            _download_and_compare(writer, current["assets"][name], local[name])

        if channel == "stable" and not test_only:
            resolver.stable.revalidate_source(verified['source'],source_token)

        # Asset downloads may be large.  Re-read the numeric Release after
        # both downloads so a concurrent contents writer cannot smuggle an
        # unexpected asset into the draft and have it made immutable by this
        # writer.  Keep this exact-state read immediately before the PATCH;
        # the main/tag fence precedes it so no other API reads widen the asset
        # race window.
        _require_current_control_and_reserved_tag(
            writer, current["tag_name"], expected_control_sha
        )
        _, pre_publish = _snapshot(writer, release_id, expected_control_sha, channel, candidate)
        if pre_publish["classification"] != resolver.COMPLETE_DRAFT:
            raise SealError(
                "audit Release changed before immutable publication"
            )
        _assert_remote_matches(pre_publish, local)
        # This is the last policy read before publication.  GitHub provides no
        # conditional Release PATCH, so the response and the repository policy
        # are checked again immediately after the write as detection fences.
        _require_immutable_releases_enabled(policy_reader)
        patch_response = writer.patch_release(
            release_id,
            {"draft": False, "prerelease": True, "make_latest": "false"},
        )
        try:
            patched = resolver._release_snapshot(
                patch_response, release_id, expected_control_sha, channel, candidate
            )
        except resolver.ResolutionError as error:
            raise SealError(
                f"audit Release publication response is invalid: {error}"
            ) from error
        if patched["classification"] != resolver.IMMUTABLE_COMPLETE:
            raise SealError(
                "audit Release publication response is not immutable and complete"
            )
        _assert_remote_matches(patched, local)
        _require_immutable_releases_enabled(policy_reader)
        _require_current_control_and_reserved_tag(
            writer, patched["tag_name"], expected_control_sha
        )

    final_raw: dict[str, Any] | None = None
    for attempt in range(max_polls):
        raw_value = writer.api_json(
            f"repos/{resolver.AUDIT_REPOSITORY}/releases/{release_id}"
        )
        if raw_value.get("draft") is False and raw_value.get("immutable") is True:
            final_raw = raw_value
            break
        if raw_value.get("draft") is True:
            raise SealError("audit Release remained or returned to draft after publication")
        if attempt + 1 < max_polls and poll_seconds:
            time.sleep(poll_seconds)
    if final_raw is None:
        raise SealError("published audit Release did not become immutable")
    try:
        final = resolver._release_snapshot(
            final_raw, release_id, expected_control_sha, channel, candidate
        )
        resolver._tag_snapshot(writer, final["tag_name"], expected_control_sha)
    except resolver.ResolutionError as error:
        raise SealError(str(error)) from error
    if final["classification"] != resolver.IMMUTABLE_COMPLETE:
        raise SealError("audit Release is not immutable and complete")
    _assert_remote_matches(final, local)
    for name in (archive_name, receipt_name):
        _download_and_compare(writer, final["assets"][name], local[name])
    _, revalidated = _snapshot(writer, release_id, expected_control_sha, channel, candidate)
    if revalidated != final:
        raise SealError(
            "audit Release identity, state, or asset set changed during final download"
        )
    _require_current_control_and_reserved_tag(
        writer, final["tag_name"], expected_control_sha
    )
    try:
        resolver._tag_snapshot(writer, final["tag_name"], expected_control_sha)
    except resolver.ResolutionError as error:
        raise SealError(str(error)) from error

    result = {
        "schema": SEAL_SCHEMA,
        "repository": resolver.AUDIT_REPOSITORY,
        "release_id": release_id,
        "tag_name": final["tag_name"],
        "control_sha": expected_control_sha,
        "initial_classification": initial_classification,
        "final_classification": final["classification"],
        "immutable": True,
        "assets": [
            {
                "id": final["assets"][name]["id"],
                "name": name,
                "size": local[name]["size"],
                "sha256": local[name]["sha256"],
            }
            for name in (archive_name, receipt_name)
        ],
        "id_bound_downloads_verified": True,
        "immutable_releases_revalidated": True,
        "remote_control_revalidated": True,
    }
    if channel == "stable":
        result.update(schema="wukongim/package-audit-release-seal/v2",channel=channel,
                      test_only=test_only,candidate=candidate,source_verified=not test_only)
    return result


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Fill and seal one exact numeric native-package audit Release."
    )
    parser.add_argument("--channel",choices=("preview","stable"),default="preview")
    parser.add_argument("--test-only",action="store_true")
    parser.add_argument("--candidate-plan",type=Path)
    parser.add_argument("--control-root",type=Path)
    parser.add_argument("--candidate-source-root",type=Path)
    parser.add_argument("--apt-receipt",type=Path)
    parser.add_argument("--rpm-receipt",type=Path)
    parser.add_argument("--source-token-env",default=None)
    parser.add_argument("--release-id", required=True, type=int)
    parser.add_argument("--expected-control-sha", required=True)
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--receipt", required=True, type=Path)
    parser.add_argument("--api-base-url", default="https://api.github.com")
    parser.add_argument("--download-base-url", default=None)
    parser.add_argument("--token-env", default="GITHUB_TOKEN")
    parser.add_argument("--policy-token-env", default=None)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    token = os.environ.get(args.token_env) if args.token_env else None
    policy_token = (
        os.environ.get(args.policy_token_env) if args.policy_token_env else None
    )
    if args.policy_token_env and not policy_token:
        print(
            "audit Release sealing failed: configured policy token environment "
            "variable is unset",
            file=sys.stderr,
        )
        return 1
    try:
        result = seal_audit_release(
            release_id=args.release_id,
            expected_control_sha=args.expected_control_sha,
            archive_path=args.archive,
            receipt_path=args.receipt,
            api_base_url=args.api_base_url,
            download_base_url=args.download_base_url or args.api_base_url,
            token=token,
            policy_token=policy_token,channel=args.channel,test_only=args.test_only,
            candidate_plan=args.candidate_plan,control_root=args.control_root,
            candidate_source_root=args.candidate_source_root,apt_receipt=args.apt_receipt,
            rpm_receipt=args.rpm_receipt,
            source_token=os.environ.get(args.source_token_env) if args.source_token_env else None,
        )
    except (SealError, resolver.ResolutionError,resolver.stable.StableInputError,
            resolver.stable.C.CompositionError,OSError,ValueError,TypeError,KeyError) as error:
        print(f"audit Release sealing failed: {error}", file=sys.stderr)
        return 1
    json.dump(result, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

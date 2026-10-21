#!/usr/bin/env python3
"""Classify and resolve one numeric WuKongIM/packages audit Release.

The resolver is read-only.  It accepts only the four publication states used
by the package publisher, downloads assets by numeric asset id, and re-reads
the Release after every download before returning evidence to its caller.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import ipaddress
import json
import os
import re
import shutil
import stat
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, BinaryIO


STABLE_SPEC = importlib.util.spec_from_file_location(
    "stable_seal_inputs", Path(__file__).resolve().with_name("verify-stable-seal-inputs.py"))
assert STABLE_SPEC is not None and STABLE_SPEC.loader is not None
stable = importlib.util.module_from_spec(STABLE_SPEC)
STABLE_SPEC.loader.exec_module(stable)

AUDIT_REPOSITORY = "WuKongIM/packages"
RECEIPT_SCHEMA = "wukongim/package-audit-release-resolution/v1"
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
CONTROL_SHA_RE = re.compile(r"^[0-9a-f]{40}$")
MAX_JSON_BYTES = 8 * 1024 * 1024
# The public site itself is capped at 750 MiB.  A USTAR audit asset also
# carries per-member headers and block padding, so its transport cap needs
# bounded headroom above the deployed-byte cap.
MAX_ARCHIVE_BYTES = 1024 * 1024 * 1024
MAX_RECEIPT_BYTES = 8 * 1024 * 1024

EMPTY_DRAFT = "empty_draft"
ARCHIVE_ONLY_DRAFT = "archive_only_draft"
COMPLETE_DRAFT = "complete_draft"
IMMUTABLE_COMPLETE = "immutable_complete"


class ResolutionError(RuntimeError):
    """An audit Release failed a fail-closed identity or state check."""


def expected_asset_names(release_id: int, channel: str = "preview",
                         candidate: dict[str, Any] | None = None) -> tuple[str, str]:
    if channel == "stable":
        if candidate is None:
            raise ResolutionError("stable candidate identity is required")
        return stable.AUDIT.candidate_archive_names(candidate['version'],candidate['snapshot_sha256'])
    return (
        f"wukongim-preview-r{release_id}-site.tar",
        f"wukongim-preview-r{release_id}-receipt.json",
    )


def _require_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise ResolutionError(f"{label} must be a JSON object")
    return value


def _require_bool(value: Any, label: str) -> bool:
    if not isinstance(value, bool):
        raise ResolutionError(f"{label} must be a boolean")
    return value


def _require_string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value:
        raise ResolutionError(f"{label} must be a non-empty string")
    return value


def _validated_base_url(value: str, label: str) -> str:
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ResolutionError(f"{label} must be an absolute HTTP(S) URL")
    if parsed.query or parsed.fragment:
        raise ResolutionError(f"{label} must not contain a query or fragment")
    return value.rstrip("/")


def _origin(url: str) -> tuple[str, str, int | None]:
    parsed = urllib.parse.urlsplit(url)
    return parsed.scheme, parsed.hostname or "", parsed.port


def _is_loopback_host(hostname: str) -> bool:
    if hostname == "localhost":
        return True
    try:
        return ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        return False


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(
        self,
        req: Any,
        fp: Any,
        code: int,
        msg: str,
        headers: Any,
        newurl: str,
    ) -> None:
        return None


class GitHubReader:
    def __init__(self, api_base_url: str, download_base_url: str, token: str | None,
                 *, deny_redirects: bool = False) -> None:
        self.api_base_url = _validated_base_url(api_base_url, "API base URL")
        self.download_base_url = _validated_base_url(
            download_base_url, "download base URL"
        )
        self.token = token
        self.deny_redirects = deny_redirects
        handlers = [_NoRedirect()]
        if deny_redirects:
            handlers.append(urllib.request.ProxyHandler({}))
        self._no_redirect = urllib.request.build_opener(*handlers)
        parsed_api = urllib.parse.urlsplit(self.api_base_url)
        if token and parsed_api.scheme != "https" and not _is_loopback_host(
            parsed_api.hostname or ""
        ):
            raise ResolutionError(
                "a GitHub token may be sent only over HTTPS or loopback HTTP"
            )

    def _headers(self, *, authorize: bool, accept: str) -> dict[str, str]:
        headers = {
            "Accept": accept,
            "User-Agent": "wukongim-package-audit-release-resolver/1",
            "X-GitHub-Api-Version": "2022-11-28",
        }
        if authorize and self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        return headers

    def _api_json(
        self, path: str, *, allow_not_found: bool
    ) -> dict[str, Any] | None:
        request = urllib.request.Request(
            f"{self.api_base_url}/{path.lstrip('/')}",
            headers=self._headers(
                authorize=True, accept="application/vnd.github+json"
            ),
            method="GET",
        )
        try:
            with self._no_redirect.open(request, timeout=30) as response:
                data = response.read(MAX_JSON_BYTES + 1)
        except urllib.error.HTTPError as error:
            if allow_not_found and error.code == 404:
                error.read(MAX_JSON_BYTES + 1)
                return None
            raise ResolutionError(f"GitHub API read failed for {path}: {error}") from error
        except OSError as error:
            raise ResolutionError(f"GitHub API read failed for {path}: {error}") from error
        if len(data) > MAX_JSON_BYTES:
            raise ResolutionError(f"GitHub API response is too large for {path}")
        try:
            return _require_object(json.loads(data), f"GitHub API response for {path}")
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise ResolutionError(f"GitHub API returned invalid JSON for {path}") from error

    def api_json(self, path: str) -> dict[str, Any]:
        value = self._api_json(path, allow_not_found=False)
        assert value is not None
        return value

    def api_json_optional(self, path: str) -> dict[str, Any] | None:
        return self._api_json(path, allow_not_found=True)

    def open_asset(self, asset_id: int) -> BinaryIO:
        path = f"repos/{AUDIT_REPOSITORY}/releases/assets/{asset_id}"
        url = f"{self.download_base_url}/{path}"
        authorize = _origin(url) == _origin(self.api_base_url)
        request = urllib.request.Request(
            url,
            headers=self._headers(
                authorize=authorize, accept="application/octet-stream"
            ),
            method="GET",
        )
        try:
            return self._no_redirect.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            if error.code not in {301, 302, 303, 307, 308}:
                raise ResolutionError(
                    f"audit asset {asset_id} download failed: HTTP {error.code}"
                ) from error
            if self.deny_redirects:
                raise ResolutionError("TEST ONLY asset redirects are forbidden") from error
            location = error.headers.get("Location")
            if not location:
                raise ResolutionError(
                    f"audit asset {asset_id} redirect omitted Location"
                ) from error
            redirected = urllib.parse.urljoin(url, location)
            parsed = urllib.parse.urlsplit(redirected)
            if parsed.scheme != "https" or not parsed.netloc:
                raise ResolutionError(
                    f"audit asset {asset_id} redirected to an unsafe URL"
                ) from error
            redirected_request = urllib.request.Request(
                redirected,
                headers=self._headers(
                    authorize=False, accept="application/octet-stream"
                ),
                method="GET",
            )
            try:
                return urllib.request.urlopen(redirected_request, timeout=60)
            except (OSError, urllib.error.HTTPError) as redirected_error:
                raise ResolutionError(
                    f"audit asset {asset_id} redirected download failed"
                ) from redirected_error
        except OSError as error:
            raise ResolutionError(
                f"audit asset {asset_id} download failed: {error}"
            ) from error


def expected_release_identity(release_id: int, channel: str = "preview") -> tuple[str, str]:
    return (
        f"native-package-{channel}-r{release_id}",
        f"WuKongIM {channel} package snapshot r{release_id}",
    )


def _release_snapshot(
    release: dict[str, Any], release_id: int, expected_control_sha: str,
    channel: str = "preview", candidate: dict[str, Any] | None = None,
) -> dict[str, Any]:
    if release.get("id") != release_id:
        raise ResolutionError(
            "audit Release id does not match the requested numeric id"
        )
    expected_tag, expected_name = expected_release_identity(release_id, channel)
    tag_name = _require_string(release.get("tag_name"), "audit Release tag_name")
    if tag_name != expected_tag:
        raise ResolutionError(
            f"audit Release tag_name must be the canonical numeric tag {expected_tag}"
        )
    name = _require_string(release.get("name"), "audit Release name")
    if name != expected_name:
        raise ResolutionError(
            f"audit Release name must be the canonical numeric name {expected_name}"
        )
    prerelease = _require_bool(
        release.get("prerelease"), "audit Release prerelease"
    )
    if not prerelease:
        raise ResolutionError("audit Release must be a prerelease")
    target_commitish = _require_string(
        release.get("target_commitish"), "audit Release target_commitish"
    )
    if target_commitish != expected_control_sha:
        raise ResolutionError(
            "audit Release target_commitish does not match the expected control commit"
        )
    draft = _require_bool(release.get("draft"), "audit Release draft")
    immutable = _require_bool(release.get("immutable"), "audit Release immutable")
    if draft and immutable:
        raise ResolutionError("audit Release cannot be both draft and immutable")
    if not draft and not immutable:
        raise ResolutionError("published audit Release must be immutable")
    published_at = release.get("published_at")
    if draft:
        if published_at is not None:
            raise ResolutionError("draft audit Release must not have published_at")
    else:
        published_at = _require_string(
            published_at, "immutable audit Release published_at"
        )

    archive_name, receipt_name = expected_asset_names(release_id, channel, candidate)
    expected_names = {archive_name, receipt_name}
    raw_assets = release.get("assets")
    if not isinstance(raw_assets, list):
        raise ResolutionError("audit Release assets must be an array")
    assets: dict[str, dict[str, Any]] = {}
    asset_ids: set[int] = set()
    for raw_asset in raw_assets:
        asset = _require_object(raw_asset, "audit Release asset")
        name = _require_string(asset.get("name"), "audit Release asset name")
        if name not in expected_names:
            raise ResolutionError(f"audit Release contains an unexpected asset: {name}")
        if name in assets:
            raise ResolutionError(f"audit Release contains a duplicate asset name: {name}")
        asset_id = asset.get("id")
        if not isinstance(asset_id, int) or isinstance(asset_id, bool) or asset_id <= 0:
            raise ResolutionError(
                f"audit Release asset {name} has an invalid numeric id"
            )
        if asset_id in asset_ids:
            raise ResolutionError(
                f"audit Release contains a duplicate asset id: {asset_id}"
            )
        if asset.get("state") != "uploaded":
            raise ResolutionError(f"audit Release asset is not fully uploaded: {name}")
        size = asset.get("size")
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise ResolutionError(f"audit Release asset {name} has an invalid size")
        if name == archive_name and size > MAX_ARCHIVE_BYTES:
            raise ResolutionError("audit site archive exceeds its size limit")
        if name == receipt_name and size > MAX_RECEIPT_BYTES:
            raise ResolutionError("audit receipt exceeds its size limit")
        digest = _require_string(
            asset.get("digest"), f"audit Release asset {name} digest"
        )
        if not digest.startswith("sha256:") or not SHA256_RE.fullmatch(digest[7:]):
            raise ResolutionError(
                f"audit Release asset {name} must have an API SHA-256 digest"
            )
        assets[name] = {
            "id": asset_id,
            "name": name,
            "size": size,
            "sha256": digest[7:],
        }
        asset_ids.add(asset_id)

    if channel == "stable":
        for family in ('archive','receipt'):
            expected = candidate[family]
            if expected['name'] in assets:
                actual = assets[expected['name']]
                if any(actual[key] != expected[key] for key in ('name','size','sha256')):
                    raise ResolutionError("remote candidate asset differs from external anchor")
    names = set(assets)
    if draft:
        if not names:
            classification = EMPTY_DRAFT
        elif names == {archive_name}:
            classification = ARCHIVE_ONLY_DRAFT
        elif names == {receipt_name}:
            raise ResolutionError("receipt-only audit draft is not recoverable")
        elif names == expected_names:
            classification = COMPLETE_DRAFT
        else:  # Kept fail-closed even if the expected set changes later.
            raise ResolutionError("audit draft has a conflicting asset set")
    else:
        if names != expected_names:
            raise ResolutionError(
                "immutable audit Release must contain exactly the archive and receipt"
            )
        classification = IMMUTABLE_COMPLETE

    return {
        "id": release_id,
        "tag_name": tag_name,
        "name": expected_name if channel == "stable" else name,
        "prerelease": prerelease,
        "target_commitish": target_commitish,
        "draft": draft,
        "immutable": immutable,
        "published_at": published_at,
        "classification": classification,
        "assets": assets,
    }


def _tag_snapshot(
    reader: GitHubReader, tag_name: str, expected_control_sha: str
) -> dict[str, str]:
    encoded_tag = urllib.parse.quote(tag_name, safe="")
    value = reader.api_json(
        f"repos/{AUDIT_REPOSITORY}/git/ref/tags/{encoded_tag}"
    )
    if value.get("ref") != f"refs/tags/{tag_name}":
        raise ResolutionError("audit Release tag ref is not the exact canonical ref")
    target = _require_object(value.get("object"), "audit Release tag target")
    if target.get("type") != "commit":
        raise ResolutionError("audit Release tag must be a lightweight commit tag")
    sha = _require_string(target.get("sha"), "audit Release tag commit")
    if sha != expected_control_sha:
        raise ResolutionError(
            "audit Release tag does not peel to the expected control commit"
        )
    return {"ref": f"refs/tags/{tag_name}", "type": "commit", "sha": sha}


def _require_tag_absent(reader: GitHubReader, tag_name: str) -> None:
    encoded_tag = urllib.parse.quote(tag_name, safe="")
    if reader.api_json_optional(
        f"repos/{AUDIT_REPOSITORY}/git/ref/tags/{encoded_tag}"
    ) is not None:
        raise ResolutionError(
            "canonical audit tag must remain absent before control binding"
        )


def _prepare_output_directory(output_dir: Path) -> bool:
    try:
        if os.path.lexists(output_dir):
            metadata = output_dir.lstat()
            if (
                stat.S_ISLNK(metadata.st_mode)
                or not stat.S_ISDIR(metadata.st_mode)
                or any(output_dir.iterdir())
            ):
                raise ResolutionError(
                    "output directory must not exist or must be an empty real directory"
                )
            return False
        output_dir.mkdir(mode=0o700, parents=False)
        return True
    except OSError as error:
        raise ResolutionError(f"cannot prepare output directory: {error}") from error


def _download_asset(
    reader: GitHubReader, asset: dict[str, Any], destination: Path
) -> dict[str, Any]:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        descriptor = os.open(destination, flags, 0o600)
    except OSError as error:
        raise ResolutionError(
            f"cannot create downloaded audit asset {asset['name']}: {error}"
        ) from error
    digest = hashlib.sha256()
    total = 0
    try:
        with os.fdopen(descriptor, "wb") as output, reader.open_asset(
            asset["id"]
        ) as response:
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                total += len(chunk)
                if total > asset["size"]:
                    raise ResolutionError(
                        f"downloaded audit asset exceeds API size: {asset['name']}"
                    )
                digest.update(chunk)
                output.write(chunk)
    except Exception:
        destination.unlink(missing_ok=True)
        raise
    actual_sha256 = digest.hexdigest()
    if total != asset["size"]:
        destination.unlink(missing_ok=True)
        raise ResolutionError(
            f"downloaded audit asset size conflicts with API: {asset['name']}"
        )
    if actual_sha256 != asset["sha256"]:
        destination.unlink(missing_ok=True)
        raise ResolutionError(
            f"downloaded audit asset digest conflicts with API: {asset['name']}"
        )
    return {**asset, "downloaded_file": asset["name"]}


def resolve_audit_release(
    *,
    release_id: int,
    expected_control_sha: str,
    output_dir: Path,
    api_base_url: str,
    download_base_url: str,
    token: str | None,
    expected_tag_state: str = "auto",
    channel: str = "preview",
    test_only: bool = False,
    candidate_plan: Path | None = None,
    candidate_receipt: Path | None = None,
) -> dict[str, Any]:
    """Classify, download, and revalidate one numeric package audit Release."""

    if not isinstance(release_id, int) or isinstance(release_id, bool) or release_id <= 0:
        raise ResolutionError("release id must be a positive integer")
    if not isinstance(expected_control_sha, str) or not CONTROL_SHA_RE.fullmatch(
        expected_control_sha
    ):
        raise ResolutionError("expected control SHA must be a lowercase 40-hex commit")
    if expected_tag_state not in {"auto", "absent", "exact"}:
        raise ResolutionError("expected tag state must be auto, absent, or exact")
    try:
        stable.network_mode(channel, test_only, api_base_url, download_base_url)
    except stable.StableInputError as error:
        raise ResolutionError(str(error)) from error
    candidate = None
    if channel == "stable":
        try:
            candidate = stable.candidate_identity(candidate_plan,candidate_receipt,test_only=test_only)
        except stable.StableInputError as error:
            raise ResolutionError(str(error)) from error
    elif candidate_plan is not None or candidate_receipt is not None:
        raise ResolutionError("candidate inputs require explicit stable mode")
    output_dir = Path(output_dir)
    reader = GitHubReader(api_base_url, download_base_url, token, deny_redirects=test_only)
    created_output = _prepare_output_directory(output_dir)
    created_assets: list[Path] = []
    release_path = f"repos/{AUDIT_REPOSITORY}/releases/{release_id}"
    try:
        initial = _release_snapshot(
            reader.api_json(release_path), release_id, expected_control_sha, channel, candidate
        )
        if expected_tag_state == "absent" and not initial["draft"]:
            raise ResolutionError("only an unpublished draft may require an absent audit tag")
        initial_tag = None
        if expected_tag_state == "absent":
            _require_tag_absent(reader, initial["tag_name"])
        elif expected_tag_state == "exact" or initial["immutable"]:
            initial_tag = _tag_snapshot(
                reader, initial["tag_name"], expected_control_sha
            )
        downloaded: dict[str, dict[str, Any]] = {}
        for name in sorted(initial["assets"]):
            destination = output_dir / name
            downloaded[name] = _download_asset(
                reader, initial["assets"][name], destination
            )
            created_assets.append(destination)
        final = _release_snapshot(
            reader.api_json(release_path), release_id, expected_control_sha, channel, candidate
        )
        if final != initial:
            raise ResolutionError(
                "audit Release identity, state, or asset set changed during resolution"
            )
        if expected_tag_state == "absent":
            _require_tag_absent(reader, initial["tag_name"])
        elif initial_tag is not None:
            final_tag = _tag_snapshot(
                reader, initial["tag_name"], expected_control_sha
            )
            if final_tag != initial_tag:
                raise ResolutionError(
                    "audit Release tag target changed during resolution"
                )
        receipt_assets = [downloaded[name] for name in sorted(downloaded)]
        result = {
            "schema": RECEIPT_SCHEMA,
            "repository": AUDIT_REPOSITORY,
            "release_id": release_id,
            "tag_name": initial["tag_name"],
            "name": initial["name"],
            "prerelease": initial["prerelease"],
            "control_sha": expected_control_sha,
            "tag_commit_verified": initial_tag is not None,
            "classification": initial["classification"],
            "draft": initial["draft"],
            "immutable": initial["immutable"],
            "published_at": initial["published_at"],
            "asset_count": len(receipt_assets),
            "total_size": sum(asset["size"] for asset in receipt_assets),
            "assets": receipt_assets,
            "release_revalidated": True,
            "id_bound_downloads": True,
        }
        if channel == "stable":
            result.update(schema="wukongim/package-audit-release-resolution/v2",
                          channel=channel,test_only=test_only,candidate=candidate)
        return result
    except Exception:
        for path in created_assets:
            path.unlink(missing_ok=True)
        if created_output:
            shutil.rmtree(output_dir, ignore_errors=True)
        raise


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Classify and resolve one numeric WuKongIM/packages audit Release."
    )
    parser.add_argument("--channel", choices=("preview","stable"), default="preview")
    parser.add_argument("--test-only", action="store_true")
    parser.add_argument("--candidate-plan", type=Path)
    parser.add_argument("--candidate-receipt", type=Path)
    parser.add_argument("--release-id", required=True, type=int)
    parser.add_argument("--expected-control-sha", required=True)
    parser.add_argument(
        "--expected-tag-state",
        choices=("auto", "absent", "exact"),
        default="auto",
        help="required canonical audit-tag state; auto preserves legacy state-based behavior",
    )
    parser.add_argument("--output-dir", required=True, type=Path)
    parser.add_argument("--api-base-url", default="https://api.github.com")
    parser.add_argument(
        "--download-base-url",
        default=None,
        help="asset API base URL; defaults to --api-base-url",
    )
    parser.add_argument(
        "--token-env",
        default="GITHUB_TOKEN",
        help="environment variable containing a packages-repository token",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    token = os.environ.get(args.token_env) if args.token_env else None
    try:
        receipt = resolve_audit_release(
            release_id=args.release_id,
            expected_control_sha=args.expected_control_sha,
            output_dir=args.output_dir,
            api_base_url=args.api_base_url,
            download_base_url=args.download_base_url or args.api_base_url,
            token=token,
            expected_tag_state=args.expected_tag_state,
            channel=args.channel,test_only=args.test_only,
            candidate_plan=args.candidate_plan,candidate_receipt=args.candidate_receipt,
        )
    except ResolutionError as error:
        print(f"audit Release resolution failed: {error}", file=sys.stderr)
        return 1
    json.dump(receipt, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

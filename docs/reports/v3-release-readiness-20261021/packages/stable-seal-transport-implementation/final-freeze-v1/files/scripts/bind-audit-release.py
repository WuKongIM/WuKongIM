#!/usr/bin/env python3
"""Bind one empty numeric package audit draft to the reviewed control commit.

This is a deliberately narrow GitHub writer.  It can change only
``target_commitish`` on an empty, canonical, unpublished audit draft, reserve
that draft's canonical lightweight audit tag at the reviewed control commit,
and restore ``tag_name`` when GitHub detaches the draft while materializing the
same tag.  The caller is responsible for proving that the previous commit is
an ancestor of the new protected ``main`` commit before invoking this program.
"""

from __future__ import annotations

import argparse
import importlib.util
from pathlib import Path
import ipaddress
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any


STABLE_SPEC = importlib.util.spec_from_file_location(
    "bind_stable_inputs", Path(__file__).resolve().with_name("verify-stable-seal-inputs.py"))
assert STABLE_SPEC is not None and STABLE_SPEC.loader is not None
stable = importlib.util.module_from_spec(STABLE_SPEC)
STABLE_SPEC.loader.exec_module(stable)

AUDIT_REPOSITORY = "WuKongIM/packages"
RESULT_SCHEMA = "wukongim/package-audit-release-binding/v1"
CONTROL_SHA_RE = re.compile(r"^[0-9a-f]{40}$")
GITHUB_DETACHED_TAG_RE = re.compile(r"^untagged-[0-9a-f]{20}$")
MAX_JSON_BYTES = 8 * 1024 * 1024


class BindingError(RuntimeError):
    """The requested audit draft binding was not safe to perform."""


def expected_release_identity(release_id: int, channel: str = "preview") -> tuple[str, str]:
    return (
        f"native-package-{channel}-r{release_id}",
        f"WuKongIM {channel} package snapshot r{release_id}",
    )


def _require_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise BindingError(f"{label} must be a JSON object")
    return value


def _require_string(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value:
        raise BindingError(f"{label} must be a non-empty string")
    return value


def _is_loopback(hostname: str) -> bool:
    if hostname == "localhost":
        return True
    try:
        return ipaddress.ip_address(hostname).is_loopback
    except ValueError:
        return False


def _validated_api_base(value: str) -> str:
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise BindingError("API base URL must be an absolute HTTP(S) URL")
    if parsed.query or parsed.fragment:
        raise BindingError("API base URL must not contain a query or fragment")
    if parsed.scheme != "https" and not _is_loopback(parsed.hostname or ""):
        raise BindingError("GitHub credentials may be sent only over HTTPS or loopback HTTP")
    return value.rstrip("/")


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


class GitHubWriter:
    """Minimal authenticated JSON client with redirects disabled."""

    def __init__(self, api_base_url: str, token: str, *, test_only: bool = False) -> None:
        if not token:
            raise BindingError("a GitHub token is required")
        self.api_base_url = _validated_api_base(api_base_url)
        self.token = token
        handlers = [_NoRedirect()]
        if test_only:
            handlers.append(urllib.request.ProxyHandler({}))
        self._opener = urllib.request.build_opener(*handlers)

    def request_json(
        self,
        method: str,
        path: str,
        payload: dict[str, Any] | None = None,
        *,
        allow_not_found: bool = False,
        allow_unprocessable: bool = False,
    ) -> dict[str, Any] | None:
        if method not in {"GET", "PATCH", "POST"}:
            raise BindingError(f"unsupported GitHub API method: {method}")
        if allow_not_found and method != "GET":
            raise BindingError("only a GitHub API GET may allow a missing response")
        if method != "GET" and hasattr(self, "control_guard"):
            self.control_guard()
        body = None
        if payload is not None:
            body = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()
        request = urllib.request.Request(
            f"{self.api_base_url}/{path.lstrip('/')}",
            data=body,
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {self.token}",
                "Content-Type": "application/json",
                "User-Agent": "wukongim-package-audit-release-binder/1",
                "X-GitHub-Api-Version": "2022-11-28",
            },
            method=method,
        )
        try:
            with self._opener.open(request, timeout=30) as response:
                data = response.read(MAX_JSON_BYTES + 1)
        except urllib.error.HTTPError as error:
            if allow_not_found and error.code == 404:
                error.read(MAX_JSON_BYTES + 1)
                return None
            if allow_unprocessable and error.code == 422:
                error.read(MAX_JSON_BYTES + 1)
                return None
            raise BindingError(
                f"GitHub API {method} failed for {path}: {error}"
            ) from error
        except OSError as error:
            raise BindingError(
                f"GitHub API {method} failed for {path}: {error}"
            ) from error
        if len(data) > MAX_JSON_BYTES:
            raise BindingError(f"GitHub API response is too large for {path}")
        try:
            value = _require_object(json.loads(data), f"GitHub API response for {path}")
            if method != "GET" and hasattr(self, "control_guard"):
                self.control_guard()
            return value
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise BindingError(f"GitHub API returned invalid JSON for {path}") from error

    def api_json(self, path: str) -> dict[str, Any]:
        return _require_object(self.request_json("GET",path), "GitHub API response")

    @staticmethod
    def _validate_lightweight_tag(
        value: dict[str, Any] | None, tag_name: str, control_sha: str
    ) -> None:
        if value is None:
            raise BindingError("GitHub returned an empty audit tag response")
        target = _require_object(value.get("object"), "audit tag target")
        if (
            value.get("ref") != f"refs/tags/{tag_name}"
            or target.get("type") != "commit"
            or target.get("sha") != control_sha
        ):
            raise BindingError(
                "canonical audit tag does not point to the reviewed control commit"
            )

    def require_lightweight_tag(self, tag_name: str, control_sha: str) -> None:
        """Prove that the canonical lightweight tag already exists and is exact."""

        value = self.request_json(
            "GET", f"repos/{AUDIT_REPOSITORY}/git/ref/tags/{tag_name}"
        )
        self._validate_lightweight_tag(value, tag_name, control_sha)

    def require_lightweight_tag_absent(self, tag_name: str) -> None:
        """Prove that a GitHub-generated detached Release name is not a ref."""

        value = self.request_json(
            "GET",
            f"repos/{AUDIT_REPOSITORY}/git/ref/tags/{tag_name}",
            allow_not_found=True,
        )
        if value is not None:
            raise BindingError("detached audit Release tag_name must not have a Git ref")

    def ensure_lightweight_tag(self, tag_name: str, control_sha: str) -> bool:
        """Create the canonical tag once, or prove an existing ref is exact."""

        created = self.request_json(
            "POST",
            f"repos/{AUDIT_REPOSITORY}/git/refs",
            {"ref": f"refs/tags/{tag_name}", "sha": control_sha},
            allow_unprocessable=True,
        )
        if created is None:
            value = self.request_json(
                "GET", f"repos/{AUDIT_REPOSITORY}/git/ref/tags/{tag_name}"
            )
            was_created = False
        else:
            value = created
            was_created = True
        self._validate_lightweight_tag(value, tag_name, control_sha)
        return was_created


def _empty_draft_snapshot(
    release: dict[str, Any],
    release_id: int,
    expected_control_shas: set[str],
    *,
    allow_github_detached_tag: bool = False,
    channel: str = "preview",
) -> dict[str, Any]:
    if release.get("id") != release_id:
        raise BindingError("audit Release id does not match the requested numeric id")
    expected_tag, expected_name = expected_release_identity(release_id, channel)
    tag_name = release.get("tag_name")
    if tag_name != expected_tag and not (
        allow_github_detached_tag
        and isinstance(tag_name, str)
        and GITHUB_DETACHED_TAG_RE.fullmatch(tag_name)
    ):
        raise BindingError(f"audit Release tag must be {expected_tag}")
    if release.get("name") != expected_name:
        raise BindingError(f"audit Release name must be {expected_name}")
    if release.get("draft") is not True:
        raise BindingError("audit Release must remain a draft")
    if release.get("prerelease") is not True:
        raise BindingError("audit Release must remain a prerelease")
    if release.get("immutable") is not False:
        raise BindingError("audit Release must not be immutable before publication")
    if release.get("published_at") is not None:
        raise BindingError("audit Release must not be published")
    if release.get("assets") != []:
        raise BindingError("audit Release must be empty before control binding")
    target_commitish = _require_string(
        release.get("target_commitish"), "audit Release target_commitish"
    )
    if target_commitish not in expected_control_shas:
        raise BindingError(
            "audit Release target_commitish does not match the expected previous control commit"
        )
    return {
        "id": release_id,
        "tag_name": tag_name,
        "name": expected_name,
        "target_commitish": target_commitish,
        "draft": True,
        "prerelease": True,
        "immutable": False,
        "published_at": None,
        "assets": [],
    }


def bind_audit_release(
    *,
    release_id: int,
    expected_previous_control_sha: str,
    expected_control_sha: str,
    api_base_url: str,
    token: str,
    channel: str = "preview", test_only: bool = False,
    candidate_plan: Path | None = None, control_root: Path | None = None,
) -> dict[str, Any]:
    """Bind an exact empty draft and return revalidated non-secret evidence."""

    if not isinstance(release_id, int) or isinstance(release_id, bool) or release_id <= 0:
        raise BindingError("release id must be a positive integer")
    for value, label in (
        (expected_previous_control_sha, "expected previous control SHA"),
        (expected_control_sha, "expected control SHA"),
    ):
        if not isinstance(value, str) or not CONTROL_SHA_RE.fullmatch(value):
            raise BindingError(f"{label} must be a lowercase 40-hex commit")

    stable.network_mode(channel,test_only,api_base_url,api_base_url)
    if channel == "preview" and (candidate_plan is not None or control_root is not None):
        raise BindingError("candidate inputs require explicit stable mode")
    writer = GitHubWriter(api_base_url, token, test_only=test_only)
    if channel == "stable":
        stable.require(candidate_plan is not None and control_root is not None,"stable candidate control inputs required")
        stable.verify_control(reader=writer,plan_path=candidate_plan,control_root=control_root,
                              expected_control_sha=expected_control_sha,test_only=test_only)
        if not test_only:
            stable.require_ancestor(writer,expected_previous_control_sha,expected_control_sha)
        writer.control_guard = lambda: stable.current_control(writer,expected_control_sha)
    path = f"repos/{AUDIT_REPOSITORY}/releases/{release_id}"
    expected_tag, _ = expected_release_identity(release_id, channel)
    initial = _empty_draft_snapshot(
        _require_object(writer.request_json("GET", path), "initial audit Release"),
        release_id,
        {expected_previous_control_sha, expected_control_sha},
        allow_github_detached_tag=True, channel=channel,
    )
    # Reserve the create-only canonical ref before changing the Release.  A
    # conflicting ref therefore cannot leave the draft rebound but unusable.
    # If the create succeeded and a later PATCH response was lost, a retry may
    # accept only the already-exact lightweight ref.
    if initial["tag_name"] == expected_tag:
        tag_created = writer.ensure_lightweight_tag(
            expected_tag, expected_control_sha
        )
    else:
        # A retry may observe the exact intermediate state GitHub creates when
        # a real tag is materialized for a draft.  Never create a missing ref
        # from that noncanonical state; require the already-reserved exact ref.
        writer.require_lightweight_tag(expected_tag, expected_control_sha)
        writer.require_lightweight_tag_absent(initial["tag_name"])
        tag_created = False

    current = _empty_draft_snapshot(
        _require_object(
            writer.request_json("GET", path), "post-reservation audit Release"
        ),
        release_id,
        {expected_previous_control_sha, expected_control_sha},
        allow_github_detached_tag=True, channel=channel,
    )
    normalized_initial = {**initial, "tag_name": expected_tag}
    normalized_current = {**current, "tag_name": expected_tag}
    if normalized_initial != normalized_current:
        raise BindingError("audit Release identity or state changed during tag reservation")
    if current["tag_name"] != expected_tag:
        writer.require_lightweight_tag_absent(current["tag_name"])
    if initial["tag_name"] != expected_tag:
        if current["tag_name"] != initial["tag_name"]:
            raise BindingError("detached audit Release tag_name changed concurrently")
    elif current["tag_name"] != expected_tag and not tag_created:
        raise BindingError("audit Release tag_name changed without this run creating the tag")

    updated = initial["target_commitish"] != expected_control_sha
    if updated:
        previous_tag_name = current["tag_name"]
        patched = _require_object(writer.request_json(
            "PATCH", path, {"target_commitish": expected_control_sha}
        ), "patched audit Release")
        current = _empty_draft_snapshot(
            patched,
            release_id,
            {expected_control_sha},
            allow_github_detached_tag=True, channel=channel,
        )
        if current["tag_name"] != expected_tag:
            writer.require_lightweight_tag_absent(current["tag_name"])
        if (
            previous_tag_name != expected_tag
            and current["tag_name"] != previous_tag_name
        ):
            raise BindingError("detached audit Release tag_name changed during binding")

    release_tag_repaired = current["tag_name"] != expected_tag
    if release_tag_repaired:
        current = _empty_draft_snapshot(
            _require_object(
                writer.request_json("PATCH", path, {"tag_name": expected_tag}),
                "repaired audit Release",
            ),
            release_id,
            {expected_control_sha}, channel=channel,
        )

    final = _empty_draft_snapshot(
        _require_object(writer.request_json("GET", path), "final audit Release"),
        release_id,
        {expected_control_sha}, channel=channel,
    )
    expected_final = {
        **initial,
        "tag_name": expected_tag,
        "target_commitish": expected_control_sha,
    }
    if expected_final != final:
        raise BindingError("audit Release identity or state changed during control binding")
    writer.require_lightweight_tag(expected_tag, expected_control_sha)
    result = {
        "schema": RESULT_SCHEMA,
        "repository": AUDIT_REPOSITORY,
        "release_id": release_id,
        "tag_name": final["tag_name"],
        "previous_control_sha": expected_previous_control_sha,
        "control_sha": expected_control_sha,
        "updated": updated,
        "audit_tag_created": tag_created,
        "release_tag_repaired": release_tag_repaired,
        "audit_tag_reserved": True,
        "empty_draft_revalidated": True,
    }
    if channel == "stable":
        stable.current_control(writer,expected_control_sha)
        result.update(schema="wukongim/package-audit-release-binding/v2",
                      channel=channel,test_only=test_only,source_verified=False)
    return result


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Bind one empty numeric package audit draft to reviewed control."
    )
    parser.add_argument("--channel",choices=("preview","stable"),default="preview")
    parser.add_argument("--test-only",action="store_true")
    parser.add_argument("--candidate-plan",type=Path)
    parser.add_argument("--control-root",type=Path)
    parser.add_argument("--release-id", required=True, type=int)
    parser.add_argument("--expected-previous-control-sha", required=True)
    parser.add_argument("--expected-control-sha", required=True)
    parser.add_argument("--api-base-url", default="https://api.github.com")
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
        result = bind_audit_release(
            release_id=args.release_id,
            expected_previous_control_sha=args.expected_previous_control_sha,
            expected_control_sha=args.expected_control_sha,
            api_base_url=args.api_base_url,
            token=token or "",channel=args.channel,test_only=args.test_only,
            candidate_plan=args.candidate_plan,control_root=args.control_root,
        )
    except (BindingError,stable.StableInputError,stable.C.CompositionError,OSError) as error:
        print(f"audit Release binding failed: {error}", file=sys.stderr)
        return 1
    json.dump(result, sys.stdout, sort_keys=True, separators=(",", ":"))
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

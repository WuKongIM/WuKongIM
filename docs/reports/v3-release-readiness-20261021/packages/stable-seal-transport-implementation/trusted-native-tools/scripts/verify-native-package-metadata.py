#!/usr/bin/env python3
"""Verify that signed APT/RPM indexes close over every package payload."""

from __future__ import annotations

import argparse
import bz2
import gzip
import hashlib
import lzma
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET


MAX_OPEN_METADATA_SIZE = 786_432_000


class ValidationError(Exception):
    """A repository metadata or payload contract was violated."""


def local_name(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def direct_child(element: ET.Element, name: str) -> ET.Element:
    matches = [child for child in element if local_name(child.tag) == name]
    if len(matches) != 1:
        raise ValidationError(
            f"metadata element {local_name(element.tag)!r} must contain one {name!r} child"
        )
    return matches[0]


def validate_relative_path(value: str, role: str) -> tuple[str, ...]:
    if not value or value.startswith("/") or "\\" in value:
        raise ValidationError(f"{role} is not a safe relative path: {value!r}")
    parts = tuple(value.split("/"))
    if any(part in {"", ".", ".."} for part in parts):
        raise ValidationError(f"{role} is not a safe relative path: {value!r}")
    return parts


def safe_file(root: Path, relative: str, role: str) -> Path:
    parts = validate_relative_path(relative, role)
    root = root.resolve(strict=True)
    target = root.joinpath(*parts)
    resolved = target.resolve(strict=True)
    if os.path.commonpath((str(root), str(resolved))) != str(root):
        raise ValidationError(f"{role} escapes its repository root: {relative!r}")
    mode = target.lstat().st_mode
    if target.is_symlink() or not stat.S_ISREG(mode):
        raise ValidationError(f"{role} is not a regular file: {relative!r}")
    return target


def safe_directory(root: Path, relative: str, role: str) -> Path:
    parts = validate_relative_path(relative, role)
    root = root.resolve(strict=True)
    target = root.joinpath(*parts)
    resolved = target.resolve(strict=True)
    if os.path.commonpath((str(root), str(resolved))) != str(root):
        raise ValidationError(f"{role} escapes its repository root: {relative!r}")
    mode = target.lstat().st_mode
    if target.is_symlink() or not stat.S_ISDIR(mode):
        raise ValidationError(f"{role} is not a directory: {relative!r}")
    return target


def sha256_and_size(path: Path) -> tuple[str, int]:
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        while chunk := stream.read(1024 * 1024):
            digest.update(chunk)
            size += len(chunk)
    return digest.hexdigest(), size


def require_sha256(value: str, role: str) -> str:
    normalized = value.strip().lower()
    if len(normalized) != 64 or any(character not in "0123456789abcdef" for character in normalized):
        raise ValidationError(f"{role} is not a SHA-256 digest")
    return normalized


def require_nonnegative_integer(value: str | None, role: str) -> int:
    if value is None or not value.isdigit():
        raise ValidationError(f"{role} is not a non-negative integer")
    return int(value)


def compare_gzip_to_plain(compressed: Path, plain: Path, role: str) -> None:
    with gzip.open(compressed, "rb") as compressed_stream, plain.open("rb") as plain_stream:
        while True:
            compressed_chunk = compressed_stream.read(1024 * 1024)
            plain_chunk = plain_stream.read(1024 * 1024)
            if compressed_chunk != plain_chunk:
                raise ValidationError(f"{role} does not expand to the signed Packages bytes")
            if not compressed_chunk:
                return


def parse_debian_control(path: Path):
    paragraph: dict[str, str] = {}
    last_key: str | None = None
    with path.open("r", encoding="utf-8", newline="") as stream:
        for raw_line in stream:
            line = raw_line.rstrip("\r\n")
            if not line:
                if paragraph:
                    yield paragraph
                    paragraph = {}
                    last_key = None
                continue
            if line[0] in " \t":
                if last_key is None:
                    raise ValidationError(f"invalid continuation line in {path}")
                paragraph[last_key] += "\n" + line[1:]
                continue
            if ":" not in line:
                raise ValidationError(f"invalid field in {path}: {line!r}")
            key, value = line.split(":", 1)
            if not key or key in paragraph:
                raise ValidationError(f"duplicate or empty field in {path}: {key!r}")
            paragraph[key] = value.lstrip()
            last_key = key
    if paragraph:
        yield paragraph


def release_sha256_paths(release: Path) -> set[str]:
    paths: set[str] = set()
    in_sha256 = False
    with release.open("r", encoding="utf-8", newline="") as stream:
        for raw_line in stream:
            line = raw_line.rstrip("\r\n")
            if line == "SHA256:":
                in_sha256 = True
                continue
            if in_sha256 and line.startswith(" "):
                fields = line.split()
                if len(fields) != 3:
                    raise ValidationError("APT Release contains a malformed SHA256 entry")
                require_sha256(fields[0], "APT Release digest")
                require_nonnegative_integer(fields[1], "APT Release size")
                validate_relative_path(fields[2], "APT Release target")
                if fields[2] in paths:
                    raise ValidationError(f"APT Release repeats a SHA256 target: {fields[2]}")
                paths.add(fields[2])
                continue
            if in_sha256:
                break
    if not paths:
        raise ValidationError("APT Release contains no SHA256 entries")
    return paths


def verify_apt_closure(repository: Path, apt_release_relative: str) -> None:
    release = safe_file(repository, apt_release_relative, "APT Release")
    release_directory = release.parent
    if release_directory.parent.name != "dists":
        raise ValidationError("APT Release must live at apt/dists/<suite>/Release")
    apt_root = release_directory.parent.parent.resolve(strict=True)
    release_paths = release_sha256_paths(release)
    release_index_paths = {
        path for path in release_paths if Path(path).name.startswith("Packages")
    }
    unsupported_release_indexes = sorted(
        path
        for path in release_index_paths
        if Path(path).name not in {"Packages", "Packages.gz"}
    )
    if unsupported_release_indexes:
        raise ValidationError(
            "APT Release authenticates unsupported Packages indexes: "
            + ", ".join(unsupported_release_indexes)
        )
    packages_paths = sorted(path for path in release_paths if path.endswith("/Packages"))
    compressed_paths = {path for path in release_paths if path.endswith("/Packages.gz")}
    if not packages_paths:
        raise ValidationError("APT Release does not authenticate an uncompressed Packages index")
    expected_compressed = {path + ".gz" for path in packages_paths}
    if compressed_paths != expected_compressed:
        raise ValidationError("APT Release must authenticate one Packages.gz for every Packages index")
    expected_indexes = set(packages_paths) | expected_compressed
    actual_indexes: set[str] = set()
    for path in release_directory.rglob("Packages*"):
        relative = path.relative_to(release_directory).as_posix()
        if path.name not in {"Packages", "Packages.gz"}:
            raise ValidationError(f"APT repository contains an unsupported Packages index: {relative}")
        if path.is_symlink() or not path.is_file():
            raise ValidationError(f"APT Packages index is not a regular file: {relative}")
        actual_indexes.add(relative)
    if actual_indexes != expected_indexes:
        raise ValidationError("APT Release does not close over the exact Packages index set")

    package_records: dict[str, tuple[int, str]] = {}
    for packages_relative in packages_paths:
        packages = safe_file(release_directory, packages_relative, "APT Packages index")
        compressed = safe_file(
            release_directory, packages_relative + ".gz", "APT compressed Packages index"
        )
        compare_gzip_to_plain(compressed, packages, packages_relative + ".gz")
        for paragraph in parse_debian_control(packages):
            missing = {"Filename", "Size", "SHA256"} - paragraph.keys()
            if missing:
                raise ValidationError(
                    f"APT package stanza is missing fields: {', '.join(sorted(missing))}"
                )
            filename = paragraph["Filename"]
            if not filename.startswith("pool/"):
                raise ValidationError(f"APT payload must live below pool/: {filename!r}")
            expected_size = require_nonnegative_integer(paragraph["Size"], "APT payload size")
            expected_digest = require_sha256(paragraph["SHA256"], "APT payload digest")
            payload = safe_file(apt_root, filename, "APT payload")
            actual_digest, actual_size = sha256_and_size(payload)
            if (actual_size, actual_digest) != (expected_size, expected_digest):
                raise ValidationError(f"APT payload digest or size mismatch: {filename}")
            previous = package_records.setdefault(filename, (expected_size, expected_digest))
            if previous != (expected_size, expected_digest):
                raise ValidationError(f"APT indexes disagree about payload: {filename}")

    if not package_records:
        raise ValidationError("APT Packages indexes contain no payloads")
    pool = apt_root / "pool"
    if not pool.is_dir() or pool.is_symlink():
        raise ValidationError("APT pool directory is missing or unsafe")
    actual_payloads: set[str] = set()
    for path in pool.rglob("*"):
        if path.is_dir():
            continue
        if path.is_symlink() or not path.is_file() or path.suffix != ".deb":
            raise ValidationError(f"APT pool contains a non-deb or unsafe file: {path}")
        actual_payloads.add(path.relative_to(apt_root).as_posix())
    if actual_payloads != set(package_records):
        raise ValidationError("APT Packages indexes do not close over the exact pool payload set")


def materialize_open_metadata(source: Path, destination: Path, expected_size: int) -> tuple[str, int]:
    process: subprocess.Popen[bytes] | None = None
    suffix = source.suffix.lower()
    if suffix == ".gz":
        source_stream = gzip.open(source, "rb")
    elif suffix == ".bz2":
        source_stream = bz2.open(source, "rb")
    elif suffix in {".xz", ".lzma"}:
        source_stream = lzma.open(source, "rb")
    elif suffix in {".zst", ".zstd"}:
        process = subprocess.Popen(
            ["zstd", "--quiet", "--decompress", "--stdout", str(source)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        if process.stdout is None:
            raise ValidationError("failed to open zstd metadata stream")
        source_stream = process.stdout
    else:
        source_stream = source.open("rb")

    digest = hashlib.sha256()
    total = 0
    try:
        with source_stream, destination.open("wb") as output:
            while chunk := source_stream.read(1024 * 1024):
                total += len(chunk)
                if total > expected_size:
                    raise ValidationError(f"opened metadata exceeds declared size: {source}")
                digest.update(chunk)
                output.write(chunk)
    except BaseException:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait()
        raise
    if process is not None:
        stderr = process.stderr.read() if process.stderr is not None else b""
        process.wait()
        if process.returncode != 0:
            raise ValidationError(
                f"zstd failed for {source}: {stderr.decode('utf-8', errors='replace').strip()}"
            )
    return digest.hexdigest(), total


def verify_rpm_closure(repository: Path, rpm_repository_relative: str) -> None:
    rpm_root = safe_directory(repository, rpm_repository_relative, "RPM repository")
    repomd = safe_file(rpm_root, "repodata/repomd.xml", "RPM repomd.xml")
    try:
        root = ET.parse(repomd).getroot()
    except ET.ParseError as error:
        raise ValidationError(f"RPM repomd.xml is invalid XML: {error}") from error

    data_entries = [element for element in root if local_name(element.tag) == "data"]
    if not data_entries:
        raise ValidationError("RPM repomd.xml contains no data entries")
    referenced: set[str] = set()
    opened_by_type: dict[str, Path] = {}
    with tempfile.TemporaryDirectory(prefix="wukongim-rpm-metadata-") as temporary:
        temporary_root = Path(temporary)
        for index, data in enumerate(data_entries):
            data_type = data.get("type", "")
            if not data_type or data_type in opened_by_type:
                raise ValidationError(f"RPM repomd.xml has an empty or duplicate data type: {data_type!r}")
            checksum = direct_child(data, "checksum")
            open_checksum = direct_child(data, "open-checksum")
            location = direct_child(data, "location")
            size = direct_child(data, "size")
            open_size = direct_child(data, "open-size")
            if checksum.get("type") != "sha256" or open_checksum.get("type") != "sha256":
                raise ValidationError(f"RPM metadata {data_type} must use SHA-256 checksums")
            expected_digest = require_sha256(checksum.text or "", f"RPM {data_type} checksum")
            expected_open_digest = require_sha256(
                open_checksum.text or "", f"RPM {data_type} open checksum"
            )
            expected_size = require_nonnegative_integer(size.text, f"RPM {data_type} size")
            expected_open_size = require_nonnegative_integer(
                open_size.text, f"RPM {data_type} open size"
            )
            if expected_open_size > MAX_OPEN_METADATA_SIZE:
                raise ValidationError(
                    f"RPM {data_type} open size exceeds the repository budget"
                )
            href = location.get("href", "")
            if not href.startswith("repodata/"):
                raise ValidationError(f"RPM metadata must live below repodata/: {href!r}")
            if href in referenced:
                raise ValidationError(f"RPM repomd.xml repeats a metadata target: {href}")
            referenced.add(href)
            metadata = safe_file(rpm_root, href, f"RPM {data_type} metadata")
            actual_digest, actual_size = sha256_and_size(metadata)
            if (actual_size, actual_digest) != (expected_size, expected_digest):
                raise ValidationError(f"RPM compressed metadata digest or size mismatch: {href}")
            opened = temporary_root / f"{index}.opened"
            actual_open_digest, actual_open_size = materialize_open_metadata(
                metadata, opened, expected_open_size
            )
            if (actual_open_size, actual_open_digest) != (
                expected_open_size,
                expected_open_digest,
            ):
                raise ValidationError(f"RPM opened metadata digest or size mismatch: {href}")
            opened_by_type[data_type] = opened

        required_types = {"primary", "filelists", "other"}
        if not required_types.issubset(opened_by_type):
            missing = ", ".join(sorted(required_types - opened_by_type.keys()))
            raise ValidationError(f"RPM repomd.xml is missing required metadata types: {missing}")
        actual_metadata: set[str] = set()
        repodata = rpm_root / "repodata"
        for path in repodata.iterdir():
            if path.name in {"repomd.xml", "repomd.xml.asc"}:
                continue
            if path.is_symlink() or not path.is_file():
                raise ValidationError(f"RPM repodata contains an unsafe entry: {path}")
            actual_metadata.add(path.relative_to(rpm_root).as_posix())
        if actual_metadata != referenced:
            raise ValidationError("RPM repomd.xml does not close over the exact repodata file set")

        try:
            primary_root = ET.parse(opened_by_type["primary"]).getroot()
        except ET.ParseError as error:
            raise ValidationError(f"RPM primary metadata is invalid XML: {error}") from error
        package_records: dict[str, tuple[int, str]] = {}
        for package in primary_root:
            if local_name(package.tag) != "package":
                continue
            if package.get("type") != "rpm":
                raise ValidationError("RPM primary metadata contains a non-rpm package")
            checksum = direct_child(package, "checksum")
            location = direct_child(package, "location")
            size = direct_child(package, "size")
            if checksum.get("type") != "sha256" or checksum.get("pkgid", "").upper() != "YES":
                raise ValidationError("RPM payload checksum must be the SHA-256 package identifier")
            expected_digest = require_sha256(checksum.text or "", "RPM payload checksum")
            expected_size = require_nonnegative_integer(size.get("package"), "RPM payload size")
            href = location.get("href", "")
            if not href.startswith("Packages/"):
                raise ValidationError(f"RPM payload must live below Packages/: {href!r}")
            if href in package_records:
                raise ValidationError(f"RPM primary metadata repeats a payload: {href}")
            payload = safe_file(rpm_root, href, "RPM payload")
            actual_digest, actual_size = sha256_and_size(payload)
            if (actual_size, actual_digest) != (expected_size, expected_digest):
                raise ValidationError(f"RPM payload digest or size mismatch: {href}")
            package_records[href] = (expected_size, expected_digest)

        if not package_records:
            raise ValidationError("RPM primary metadata contains no packages")
        packages_directory = rpm_root / "Packages"
        if not packages_directory.is_dir() or packages_directory.is_symlink():
            raise ValidationError("RPM Packages directory is missing or unsafe")
        actual_payloads: set[str] = set()
        for path in packages_directory.rglob("*"):
            if path.is_dir():
                continue
            if path.is_symlink() or not path.is_file() or path.suffix != ".rpm":
                raise ValidationError(f"RPM Packages contains a non-rpm or unsafe file: {path}")
            actual_payloads.add(path.relative_to(rpm_root).as_posix())
        if actual_payloads != set(package_records):
            raise ValidationError("RPM primary metadata does not close over the exact package set")


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repository", required=True)
    parser.add_argument("--apt-release", required=True)
    parser.add_argument("--rpm-repository", required=True)
    return parser.parse_args()


def main() -> int:
    arguments = parse_arguments()
    try:
        repository = Path(arguments.repository)
        if repository.is_symlink() or not repository.is_dir():
            raise ValidationError("repository must be a non-symbolic-link directory")
        verify_apt_closure(repository, arguments.apt_release)
        verify_rpm_closure(repository, arguments.rpm_repository)
    except (OSError, ValidationError, subprocess.SubprocessError) as error:
        print(f"native-package metadata validation failed: {error}", file=sys.stderr)
        return 65
    print("native-package metadata closure validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

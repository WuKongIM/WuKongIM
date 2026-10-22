#!/usr/bin/env python3
"""Disabled publication boundary and read-only HTTPS verification.

Upload/activation composition is not implemented. The CLI cannot enable it or
select test transports. Original fresh authentication remains the only source
verifier; probe success is a response observation, never publication authority.
"""
from __future__ import annotations

import argparse
import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import re
import selectors
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
HOST = 'packages.githubim.com'
CONTROL_PATH = ROOT / 'manifests/object-storage-publication.json'
CONTROL_LIMIT = 64 * 1024
HEADER_LIMIT = 64 * 1024
PROBE_TIMEOUT = 20


def _module(filename):
    spec = importlib.util.spec_from_file_location('publication_fresh', ROOT / 'scripts' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


# Reviewed checkout only: no paths or callbacks supplied by input JSON/CLI.
F = _module('verify-object-storage-delivery-inputs.py')
MAX_RESPONSE_BYTES = F.D.P.MAX_TOTAL_ARCHIVE_BYTES
BINDINGS = {'operation', 'channel', 'inputs_sha256', 'plan_sha256', 'targets',
            'uploader', 'activator', 'expected_configuration_sha256', 'managed_config_ids'}


class PublicationError(ValueError):
    """Publication is disabled or an exact response/control contract failed."""


def require(condition, message):
    if not condition:
        raise PublicationError(message)


def validate_current_control(value):
    """Accept only the disabled, unknown template supported by this phase."""
    require(isinstance(value, dict) and set(value) == BINDINGS | {'schema', 'backend', 'enabled'},
            'publication control fields differ')
    require(value['schema'] == 'wukongim.object_storage_publication/v1'
            and value['backend'] == 'aliyun_oss_cdn' and type(value['enabled']) is bool,
            'publication control schema/backend/enabled differs')
    require(not value['enabled'], 'production publication composition is not implemented')
    require(all(value[field] is None for field in BINDINGS),
            'disabled publication bindings must remain unknown')
    return value


def read_control_file(path):
    """Read bounded regular JSON; duplicate keys and symlinks are not controls."""
    try:
        path = Path(path)
        checked = F.C.candidate_path(path.parent, path.name, 'publication control')
        raw = F.C.read_checked(checked, 'publication control', maximum_bytes=CONTROL_LIMIT)
        return validate_current_control(json.loads(raw, object_pairs_hook=F.C.reject_duplicate_pairs))
    except (ValueError, OSError, TypeError):
        raise PublicationError('publication control unavailable or invalid') from None


def read_current_control():
    """Only the fixed executing checkout provides the current control file."""
    return read_control_file(CONTROL_PATH)


def verify_fresh_inputs(*, inputs, expected_plan_sha256, executor_sha,
                        source_token=None, package_token=None, previous=None):
    """Keep original clean Git, GitHub, attestation and native checks hardwired.

    Each future upload/activate invocation must start independently. previous is
    reserved for the first observation of the same activation, never an upload
    receipt or an external success JSON. This function has no write authority.
    """
    return F.verify_delivery_inputs(inputs=inputs, expected_plan_sha256=expected_plan_sha256,
                                   executor_sha=executor_sha, source_token=source_token,
                                   package_token=package_token, previous=previous)


def _request(uri, expected):
    require(isinstance(uri, str) and 0 < len(uri) <= 8192 and uri.startswith('/')
            and not uri.startswith('//') and all(33 <= ord(char) <= 126 for char in uri)
            and '#' not in uri and '\\' not in uri, 'probe URI must be an exact canonical-origin request')
    require(isinstance(expected, dict) and set(expected) == {'size', 'sha256', 'cache_control'},
            'probe requires exact byte and cache facts')
    require(type(expected['size']) is int and 0 <= expected['size'] <= MAX_RESPONSE_BYTES
            and isinstance(expected['sha256'], str) and re.fullmatch('[0-9a-f]{64}', expected['sha256'])
            and isinstance(expected['cache_control'], str) and 0 < len(expected['cache_control']) <= 1024
            and all(32 <= ord(char) <= 126 for char in expected['cache_control']), 'probe byte/cache facts invalid')


def _headers(raw, expected):
    """Check one final HTTP/1.x response; redirects and encoded bodies fail closed."""
    lines = raw.split(b'\r\n')
    require(len(lines) <= 101 and re.fullmatch(rb'HTTP/1\.[01] 200(?: [\x20-\x7e]*)?', lines[0]),
            'probe response is not HTTP 200')
    selected = {}
    for line in lines[1:]:
        name, separator, value = line.partition(b':')
        require(separator and re.fullmatch(rb'[!#$%&\x27*+.^_`|~0-9A-Za-z-]+', name),
                'probe response header invalid')
        key = name.lower()
        if key in (b'cache-control', b'content-length', b'content-encoding'):
            require(key not in selected, 'probe response header duplicated')
            selected[key] = value.strip()
    require(selected.get(b'cache-control') == expected['cache_control'].encode('ascii'),
            'probe cache metadata differs')
    require(selected.get(b'content-encoding', b'identity') == b'identity',
            'probe response encoding differs')
    if b'content-length' in selected:
        require(selected[b'content-length'] == str(expected['size']).encode('ascii'),
                'probe declared size differs')


def _probe(*, uri, expected, connect_host, connect_port, fixture_ca, timeout, test_only):
    """Stream one bounded curl GET, preserving canonical Host/SNI/certificate name.

    Read at most a fixed header buffer and one body chunk at a time. An absolute
    deadline also bounds slow streaming; every exit terminates and reaps curl.
    No raw headers, TLS errors, credentials or response body enter the receipt.
    """
    _request(uri, expected)
    require(type(timeout) in (int, float) and 0 < timeout <= PROBE_TIMEOUT, 'probe timeout invalid')
    target = ('[' + connect_host + ']') if ':' in connect_host else connect_host
    args = ['curl', '--disable', '--silent', '--show-error', '--http1.1', '--proto', '=https',
            '--globoff', '--path-as-is', '--no-buffer', '--noproxy', '*', '--proxy', '',
            '--connect-timeout', str(min(5, timeout)), '--max-time', str(timeout),
            '--include', '--header', 'Accept-Encoding: identity', '--url', 'https://' + HOST + uri]
    if connect_host != HOST or connect_port != 443:
        args += ['--connect-to', f'{HOST}:443:{target}:{connect_port}']
    if fixture_ca is not None:
        args += ['--cacert', str(fixture_ca)]
    process = None
    selector = selectors.DefaultSelector()
    deadline = time.monotonic() + timeout
    header = bytearray()
    header_done = False
    size = 0
    digest = hashlib.sha256()
    try:
        process = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.DEVNULL,
                                   env={'PATH': os.environ.get('PATH', os.defpath), 'LC_ALL': 'C'})
        os.set_blocking(process.stdout.fileno(), False)
        selector.register(process.stdout, selectors.EVENT_READ)
        while True:
            remaining = deadline - time.monotonic()
            require(remaining > 0, 'probe deadline exceeded')
            if not selector.select(min(remaining, 0.1)):
                continue
            chunk = os.read(process.stdout.fileno(), 65536)
            if not chunk:
                require(process.wait(timeout=max(0.001, deadline - time.monotonic())) == 0,
                        'HTTPS probe failed')
                break
            if not header_done:
                header.extend(chunk)
                end = header.find(b'\r\n\r\n')
                require((end if end >= 0 else len(header)) <= HEADER_LIMIT, 'probe headers exceed limit')
                if end < 0:
                    continue
                _headers(bytes(header[:end]), expected)
                chunk = bytes(header[end + 4:])
                header.clear()
                header_done = True
            size += len(chunk)
            require(size <= expected['size'], 'probe body exceeds exact size')
            digest.update(chunk)
        require(header_done and size == expected['size'] and digest.hexdigest() == expected['sha256'],
                'probe complete body differs')
        return {'uri': uri, 'size': size, 'sha256': digest.hexdigest(),
                'cache_control': expected['cache_control'], 'canonical_host': HOST,
                'connection_target': connect_host, 'response_verified': True, 'test_only': test_only,
                'public_verified': False, 'production_enabled': False, 'publication_verified': False}
    except (OSError, subprocess.SubprocessError):
        raise PublicationError('HTTPS probe did not complete') from None
    finally:
        selector.close()
        if process is not None:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=1)
            process.stdout.close()


def probe_public(*, uri, expected, cname=None):
    """Read the canonical public origin, optionally via its reviewed CDN CNAME.

    The hostname used for Host, SNI and TLS certificate validation never changes.
    A pre-cutover CNAME is a connection destination, not a replacement origin.
    """
    require(cname is None or (isinstance(cname, str) and len(cname) <= 253
            and re.fullmatch(r'(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+kunlunaq\.com', cname)),
            'probe CNAME must be an explicit CDN destination')
    return _probe(uri=uri, expected=expected, connect_host=cname or HOST, connect_port=443,
                  fixture_ca=None, timeout=PROBE_TIMEOUT, test_only=False)


def probe_test_only(*, uri, expected, connect_host, connect_port, fixture_ca, timeout=PROBE_TIMEOUT):
    """Explicit loopback fixture seam, unreachable from the production CLI."""
    try:
        loopback = ipaddress.ip_address(connect_host).is_loopback
    except ValueError:
        loopback = False
    require(loopback and type(connect_port) is int and 0 < connect_port < 65536,
            'fixture transport requires loopback and a valid port')
    require(isinstance(fixture_ca, Path) and fixture_ca.is_file() and not fixture_ca.is_symlink(),
            'fixture transport requires its owned CA certificate')
    return _probe(uri=uri, expected=expected, connect_host=connect_host, connect_port=connect_port,
                  fixture_ca=fixture_ca, timeout=timeout, test_only=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--operation', choices=('upload', 'activate'), required=True)
    parser.add_argument('--inputs', type=Path, required=True)
    parser.add_argument('--executor-sha', required=True)
    parser.add_argument('--output-dir', type=Path, required=True)
    parser.parse_args(argv)
    try:
        read_current_control()
        raise PublicationError('production object publication is disabled; upload/activate composition is not implemented')
    except PublicationError as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())

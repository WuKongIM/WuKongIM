#!/usr/bin/env python3
"""One reviewed local operation; no publication, cloud access, or existing secret reuse."""
from __future__ import annotations

import argparse
import base64
import hashlib
import getpass
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import signal
import stat
import subprocess
import sys
import tempfile
import time

sys.dont_write_bytecode = True
GPG = '/opt/homebrew/bin/gpg'
GPGCONF = '/opt/homebrew/bin/gpgconf'
SECURITY = '/usr/bin/security'
SOURCE_ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-local-request')
DEFAULT_TARGET = Path.home() / 'Library/Application Support/WuKongIM/stable-signing'
# This exact reviewed validator is reused, including current-only and wrong-passphrase proof.
VALIDATOR_SHA256 = '2f525f247384b1c9fb014ac7587be7b807dc0901a40ff964984cade238a4ffc4'
SAFE_TOKEN = re.compile(r'^[A-Za-z0-9._/-]+$')
FINGERPRINT = re.compile(r'^[0-9A-F]{40}$')
MAX_NATIVE_OUTPUT = 2 * 1024 * 1024


class PreparationError(Exception):
    """A stage-only error; never include raw tool output or secret input."""


def require(condition, message):
    if not condition:
        raise PreparationError(message)


def run_command(argv, *, stage, input_bytes=b'', check=True, timeout=300):
    """Run trusted local tools without shell expansion or secret-bearing argv/logs."""
    try:
        result = subprocess.run(
            argv, input=input_bytes, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            timeout=timeout, check=False,
            env={'PATH': '/opt/homebrew/bin:/usr/bin:/bin', 'HOME': str(Path.home()),
                 'LANG': 'C', 'LC_ALL': 'C'},
        )
    except (OSError, subprocess.TimeoutExpired):
        raise PreparationError(stage + ' could not complete') from None
    require(len(result.stdout) <= MAX_NATIVE_OUTPUT and len(result.stderr) <= MAX_NATIVE_OUTPUT,
            stage + ' exceeded output bound')
    if check and result.returncode:
        raise PreparationError(stage + ' failed (exit ' + str(result.returncode) + ')')
    return result


def gpg_argv(home, arguments, password=False):
    argv = [GPG, '--no-options', '--homedir', str(home), '--batch', '--no-tty',
            '--no-auto-key-retrieve', '--auto-key-locate', 'clear']
    if password:
        argv += ['--pinentry-mode', 'loopback', '--passphrase-fd', '0']
    return argv + list(arguments)


def gpg(run, home, arguments, *, stage, password=None, check=True):
    return run(gpg_argv(home, arguments, password is not None), stage=stage,
               input_bytes=b'' if password is None else bytes(password) + b'\n', check=check)


def kill_agent(run, home):
    run([GPGCONF, '--homedir', str(home), '--kill', 'all'],
        stage='owned GPG home cleanup', check=True, timeout=30)


def create_target(path):
    path = Path(path)
    require(not os.path.lexists(path), 'target already exists; manual inspection required')
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    try:
        path.mkdir(mode=0o700)
    except OSError:
        raise PreparationError('cannot exclusively create target') from None
    require(path.stat().st_mode & 0o777 == 0o700, 'target permissions are not 0700')


def write_private(path, contents):
    require(contents, 'refusing an empty retained artifact')
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        stream.write(contents)


def write_receipt(target, receipt):
    # This target was exclusively created by this invocation. Never write secret values.
    temporary = target / 'public-receipt.json.tmp'
    with temporary.open('w') as stream:
        json.dump(receipt, stream, indent=2, sort_keys=True)
        stream.write('\n')
    temporary.chmod(0o600)
    temporary.replace(target / 'public-receipt.json')


def resolve_keychain(run):
    result = run([SECURITY, 'default-keychain', '-d', 'user'], stage='default Keychain identity')
    try:
        path = json.loads(result.stdout.decode('utf-8'))
    except (ValueError, UnicodeError):
        raise PreparationError('cannot resolve exact default Keychain identity') from None
    require(isinstance(path, str) and path.startswith('/') and SAFE_TOKEN.fullmatch(path),
            'default Keychain path needs operator review before interactive command use')
    return path


def require_keychain_absent(run, service, account, keychain):
    require(SAFE_TOKEN.fullmatch(service) and SAFE_TOKEN.fullmatch(account),
            'Keychain identity contains unsafe command characters')
    result = run([SECURITY, 'find-generic-password', '-s', service, '-a', account, keychain],
                 stage='Keychain metadata existence check', check=False)
    # Apple's find wrapper may collapse some search errors into item-not-found.
    # This is a visibility check; add without -U remains the atomic no-overwrite boundary.
    require(result.returncode == 44, 'Keychain item exists or lookup is inconclusive')
    stderr = result.stderr.lower()
    require(b'sec keychainsearchcreatefromattributes' not in stderr
            and b'seckeychainsearchcreatefromattributes' not in stderr,
            'Keychain lookup reported a search error')


def create_keychain_item(run, service, account, password, keychain):
    require(SAFE_TOKEN.fullmatch(service) and SAFE_TOKEN.fullmatch(account),
            'unsafe Keychain identity')
    require(isinstance(keychain, str) and keychain.startswith('/') and SAFE_TOKEN.fullmatch(keychain),
            'unsafe frozen Keychain path')
    require(re.fullmatch(rb'[0-9a-f]{64}', bytes(password)), 'invalid generated passphrase')
    # Exactly one command, one final newline and EOF. No -U, -v, trailing help or empty line.
    command = (f'add-generic-password -s {service} -a {account} -w '.encode()
               + bytes(password) + b' ' + keychain.encode('ascii') + b'\n')
    added = run([SECURITY, '-i'], stage='new Keychain item creation', input_bytes=command,
                check=False)
    require(added.returncode == 0, 'Keychain creation failed; no password retrieval attempted')
    # Read only after this exact invocation's successful atomic Create, never after failure.
    stored = run([SECURITY, 'find-generic-password', '-s', service, '-a', account, '-w', keychain],
                 stage='new Keychain item verification').stdout
    if stored.endswith(b'\n'):
        stored = stored[:-1]
    require(secrets.compare_digest(stored, bytes(password)), 'new Keychain item did not verify')


def require_distinct(fingerprints):
    require(all(FINGERPRINT.fullmatch(v) for v in fingerprints), 'invalid public fingerprint')
    for length in (40, 16, 8):
        require(len({value[-length:] for value in fingerprints}) == len(fingerprints),
                'public fingerprint or short key ID collision')


def require_full_restore(records, expected):
    actual = [r for r in records if r.record_type in ('sec', 'ssb')]
    require(len(actual) == 3 and [r.record_type for r in actual] == ['sec', 'ssb', 'ssb']
            and {r.fingerprint for r in actual} == set(expected)
            and all(r.secret_marker in ('', '+') for r in actual),
            'full backup did not restore all three local private keys')


def require_valid_signature(output, signer, primary):
    lines = [line.split() for line in output.decode('ascii', errors='replace').splitlines()
             if line.startswith('[GNUPG:] VALIDSIG ')]
    require(len(lines) == 1 and len(lines[0]) >= 12
            and lines[0][2] == signer and lines[0][9] == '8'
            and lines[0][-1] == primary, 'signature proof failed exact signer or SHA256 check')


def export_current(run, home, current, password):
    result = gpg(run, home, ['--export-secret-subkeys', current + '!'],
                 stage='encrypted current-only export', password=password).stdout
    require(result, 'current-only export is empty')
    return result


def list_records(run, validator, home, *, secret=False):
    result = gpg(run, home, ['--with-colons', '--fixed-list-mode', '--with-fingerprint',
                            '--with-subkey-fingerprint',
                            '--list-secret-keys' if secret else '--list-keys'],
                 stage='restored secret inventory' if secret else 'public topology').stdout
    return validator.parse_colon_keys(result, allow_no_keys=secret)


def load_validator(source):
    path = source / 'scripts/validate-signing-material.py'
    require(hashlib.sha256(path.read_bytes()).hexdigest() == VALIDATOR_SHA256,
            'reviewed validator source changed')
    spec = importlib.util.spec_from_file_location('stable_operator_validator', path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def preview_fingerprints(source):
    data = json.loads((source / 'manifests/preview-signing.json').read_text())
    values = []
    for family in ('apt', 'rpm'):
        key = data[family]
        for value in [key['primary_fingerprint'], key['signing_subkeys']['current'],
                      key['signing_subkeys']['next'], *key['signing_subkeys']['historical']]:
            if value is not None:
                values.append(value)
    require_distinct(values)
    return values


def run_current_validator(validator, args, run):
    """Retain exact repository validation; only route its native I/O through bounded runner."""
    original = validator.IsolatedGPG

    class OperatorGPG(original):
        def run(self, arguments, *, stage, input_bytes=None, check=True):
            return run([self.gpg, '--no-options', '--homedir', str(self.home), '--batch',
                        '--no-tty', *arguments], stage='existing validator: ' + stage,
                       input_bytes=b'' if input_bytes is None else input_bytes, check=check)

        def kill_agent(self):
            kill_agent(run, self.home)

    validator.IsolatedGPG = OperatorGPG
    try:
        return validator.validate_material(args)
    finally:
        validator.IsolatedGPG = original


def prove_subkey(run, private_home, public_home, scratch, public_dir, family,
                 role, fingerprint, primary, password):
    payload = scratch / (family + '-' + role + '-proof.bin')
    signature = scratch / (family + '-' + role + '-proof.sig')
    nonce = secrets.token_bytes(48)
    write_private(payload, nonce)
    command = ['--yes', '--local-user', fingerprint + '!', '--digest-algo', 'SHA256',
               '--output', str(signature), '--detach-sign', str(payload)]
    kill_agent(run, private_home)
    wrong = (b'0' if password[:1] != b'0' else b'1') + bytes(password[1:])
    rejected = gpg(run, private_home, command, stage=role + ' wrong-passphrase proof',
                   password=wrong, check=False)
    require(rejected.returncode != 0, role + ' private key lacks passphrase protection')
    signature.unlink(missing_ok=True)
    kill_agent(run, private_home)
    gpg(run, private_home, command, stage=role + ' restored signing proof', password=password)
    verified = gpg(run, public_home, ['--status-fd', '1', '--verify', str(signature), str(payload)],
                   stage=role + ' public-only verification').stdout
    require_valid_signature(verified, fingerprint, primary)
    write_private(public_dir / (family + '-' + role + '-proof.bin'), nonce)
    write_private(public_dir / (family + '-' + role + '-proof.sig'), signature.read_bytes())
    return {'verified': True, 'fingerprint': fingerprint, 'sha256': hashlib.sha256(nonce).hexdigest()}


def prove_primary(run, validator, home, primary, original_expiry, password):
    # Only restored scratch is changed. Retained public certificate/full backup already exist.
    arguments = ['--cert-digest-algo', 'SHA256', '--quick-set-expire', primary, '366d']
    kill_agent(run, home)
    wrong = (b'0' if password[:1] != b'0' else b'1') + bytes(password[1:])
    failed = gpg(run, home, arguments, stage='primary wrong-passphrase proof',
                 password=wrong, check=False)
    require(failed.returncode != 0, 'primary private key lacks passphrase protection')
    kill_agent(run, home)
    gpg(run, home, arguments, stage='restored primary certification proof', password=password)
    records = list_records(run, validator, home)
    primaries = [r for r in records if r.record_type == 'pub']
    require(len(primaries) == 1 and primaries[0].fingerprint == primary
            and primaries[0].expires > original_expiry,
            'restored primary expiry update was not observed')
    listing = gpg(run, home, ['--with-colons', '--fixed-list-mode', '--no-sig-cache',
                             '--check-sigs', primary], stage='primary certification verification').stdout
    good = [line.split(':') for line in listing.decode('ascii', errors='replace').splitlines()
            if line.startswith('sig:')]
    require(any(len(row) > 15 and row[1] == '!' and row[12] == primary
                and row[10][:2] in ('10', '11', '12', '13') and row[15] == '8'
                for row in good), 'restored primary certification signature did not verify')
    return {'verified': True, 'original_expiry': original_expiry,
            'scratch_expiry': primaries[0].expires, 'retained_certificate_unchanged': True}


def prepare_family(run, validator, target, scratch, family, password, test_only):
    private = target / family
    private.mkdir(mode=0o700)
    public = target / 'public'
    generated, restored, verify = [scratch / (family + '-' + v) for v in ('generate', 'restore', 'verify')]
    homes = [generated, restored, verify]
    for home in homes:
        home.mkdir(mode=0o700)
    try:
        uid = 'WuKongIM ' + family.upper() + ' Stable' + (' TEST ONLY' if test_only else '')
        gpg(run, generated, ['--quick-gen-key', uid, 'rsa3072', 'cert', '365d'],
            stage=family + ' primary generation', password=password)
        records = list_records(run, validator, generated)
        primary = next(r.fingerprint for r in records if r.record_type == 'pub')
        subkeys = []
        for expiry in ('90d', '180d'):
            gpg(run, generated, ['--quick-add-key', primary, 'rsa3072', 'sign', expiry],
                stage=family + ' signing subkey generation', password=password)
            records = list_records(run, validator, generated)
            added = [r.fingerprint for r in records if r.record_type == 'sub' and r.fingerprint not in subkeys]
            require(len(added) == 1, 'generation did not add exactly one signing subkey')
            subkeys += added
        current, following = subkeys
        fingerprints = [primary, current, following]
        for r in records:
            if r.record_type in ('pub', 'sub'):
                validator.validate_rpm_rsa_key(r, 'generated RSA3072 key')
                require(r.key_bits == 3072, 'unexpected generated RSA size')
        validator.validate_public_key(records, primary, current, following, [], 30, 45, 180,
                                      int(time.time()), family)
        primary_record = next(r for r in records if r.record_type == 'pub')
        next_record = next(r for r in records if r.fingerprint == following)
        current_record = next(r for r in records if r.fingerprint == current)
        require(primary_record.expires - primary_record.created == 365 * 86400
                and current_record.expires - current_record.created == 90 * 86400
                and next_record.expires - next_record.created == 180 * 86400,
                'generated lifetimes differ from the approved fixed parameters')
        require(primary_record.expires >= next_record.expires, 'primary expires before next subkey')
        certificate = gpg(run, generated, ['--armor', '--export', primary],
                          stage='complete public certificate export').stdout
        write_private(public / (family + '-stable.asc'), certificate)
        full = gpg(run, generated, ['--export-secret-keys', primary],
                   stage='encrypted complete backup export', password=password).stdout
        backup = private / 'full-secret-backup.gpg'
        write_private(backup, full)
        revocation = generated / 'openpgp-revocs.d' / (primary + '.rev')
        rev = validator.checked_regular_file(revocation, 'generated revocation', 1024 * 1024, secret=True)
        write_private(private / 'revocation.rev', rev)
        # Fresh restore uses only the retained complete backup, never generation home's state.
        kill_agent(run, generated)
        gpg(run, restored, ['--import', str(backup)], stage='full encrypted backup restore')
        restored_public = list_records(run, validator, restored)
        require(validator.public_topology(restored_public) == validator.public_topology(records),
                'full restore changed public topology')
        require_full_restore(list_records(run, validator, restored, secret=True), fingerprints)
        selected = export_current(run, restored, current, password)
        current_path = private / 'current-secret-subkey.b64'
        write_private(current_path, base64.b64encode(selected))
        gpg(run, verify, ['--import', str(public / (family + '-stable.asc'))],
            stage='public-only verification keyring')
        require(not [r for r in list_records(run, validator, verify, secret=True)
                     if r.record_type in ('sec', 'ssb')], 'verification home contains private material')
        proofs = {}
        for role, fpr in (('current', current), ('next', following)):
            proofs[role] = prove_subkey(run, restored, verify, scratch, public, family,
                                       role, fpr, primary, password)
        certificate_hash = hashlib.sha256(certificate).hexdigest()
        backup_hash = hashlib.sha256(full).hexdigest()
        proofs['primary'] = prove_primary(run, validator, restored, primary, primary_record.expires, password)
        require(hashlib.sha256(backup.read_bytes()).hexdigest() == backup_hash
                and hashlib.sha256((public / (family + '-stable.asc')).read_bytes()).hexdigest() == certificate_hash,
                'restoration proof changed retained source material')
        # Call original helper on original full public certificate/current-only export after all restoration proofs.
        password_file = scratch / (family + '-passphrase')
        write_private(password_file, bytes(password))
        args = validator.build_parser().parse_args([
            '--family', family, '--public-cert', str(public / (family + '-stable.asc')),
            '--secret-subkey-base64-file', str(current_path), '--passphrase-file', str(password_file),
            '--primary-fingerprint', primary, '--signing-subkey-fingerprint', current,
            '--next-signing-subkey-fingerprint', following,
            '--minimum-valid-days', '30', '--rotation-begin-days', '45', '--maximum-lifetime-days', '180',
        ])
        validation = run_current_validator(validator, args, run)
        password_file.unlink()
        return {'family': family, 'primary': primary, 'current': current, 'next': following,
                'public_certificate_sha256': certificate_hash, 'full_backup_sha256': backup_hash,
                'roles': [{'fingerprint': r.fingerprint, 'created': r.created, 'expires': r.expires,
                           'bits': r.key_bits, 'algorithm': r.public_key_algorithm}
                          for r in records if r.record_type in ('pub', 'sub')],
                'full_restore_verified': True, 'proofs': proofs, 'current_validator': validation}
    finally:
        original_error = sys.exc_info()[1]
        cleanup_errors = []
        for home in homes:
            try:
                kill_agent(run, home)
            except PreparationError:
                cleanup_errors.append(home.name)
        if cleanup_errors:
            reason = str(original_error) if isinstance(original_error, PreparationError) else (type(original_error).__name__ if original_error else '')
            raise PreparationError((reason + '; ' if reason else '') + 'owned GPG home cleanup incomplete')


def prepare(args, run=run_command):
    target = args.output.expanduser().absolute()
    default = DEFAULT_TARGET.expanduser().absolute()
    require(not args.test_only or target != default, 'TEST ONLY requires a separate explicit output')
    validator = load_validator(args.source_root.resolve())
    preview = preview_fingerprints(args.source_root.resolve())
    target_created = False
    passwords = {}
    receipt = {'schema': 'wukongim/local-stable-key-preparation/v1', 'complete': False,
               'test_only': args.test_only, 'publication': False, 'stable_ci_secret_copy': False,
               'target': str(target), 'validator_sha256': VALIDATOR_SHA256,
               'operator_script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
               'preview_manifest_sha256': hashlib.sha256((args.source_root / 'manifests/preview-signing.json').read_bytes()).hexdigest(),
               'keychain_items': [], 'families': []}
    try:
        keychain = resolve_keychain(run)
        receipt['keychain_path'] = keychain
        create_target(target)
        target_created = True
        (target / 'public').mkdir(mode=0o700)
        write_receipt(target, receipt)
        suffix = '-TEST-ONLY-' + hashlib.sha256(str(target).encode()).hexdigest()[:12] if args.test_only else ''
        local_account = getpass.getuser()
        require(SAFE_TOKEN.fullmatch(local_account), 'local account label needs operator review')
        receipt['local_account'] = local_account
        receipt['local_account_is_custodian_attestation'] = False
        identities = [(family, 'WuKongIM-Stable-' + family.upper() + suffix, local_account)
                      for family in ('apt', 'rpm')]
        for _, service, account in identities:
            require_keychain_absent(run, service, account, keychain)
        # Persist fresh independent passphrases before generation so partial failures are recoverable.
        for family, service, account in identities:
            passwords[family] = bytearray(secrets.token_hex(32).encode('ascii'))
            entry = {'family': family, 'service': service, 'account': account, 'status': 'creation_started'}
            receipt['keychain_items'].append(entry)
            write_receipt(target, receipt)
            create_keychain_item(run, service, account, passwords[family], keychain)
            entry['status'] = 'created_and_verified'
            write_receipt(target, receipt)
        # Short /tmp paths avoid macOS gpg-agent socket path limits. This scope owns only these homes.
        with tempfile.TemporaryDirectory(prefix='wk-stable-keys-', dir='/tmp') as temporary:
            scratch = Path(temporary)
            scratch.chmod(0o700)
            for family in ('apt', 'rpm'):
                result = prepare_family(run, validator, target, scratch, family, passwords[family], args.test_only)
                receipt['families'].append(result)
                all_fingerprints = preview + [f[r] for f in receipt['families'] for r in ('primary', 'current', 'next')]
                require_distinct(all_fingerprints)
                write_receipt(target, receipt)
        receipt['complete'] = True
        receipt['completed_at_unix'] = int(time.time())
        write_receipt(target, receipt)
        return receipt
    except BaseException as error:
        # No raw subprocess output/path-bearing traceback or random secret enters public diagnostics.
        receipt['error'] = str(error) if isinstance(error, PreparationError) else type(error).__name__
        receipt['status'] = 'incomplete_operator_inspection_required'
        if target_created:
            write_receipt(target, receipt)
        return receipt
    finally:
        for password in passwords.values():
            password[:] = b'\0' * len(password)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute', action='store_true', help='explicitly perform the reviewed local operation')
    parser.add_argument('--test-only', action='store_true', help='separate temporary identity; still creates new Keychain entries')
    parser.add_argument('--output', type=Path, default=DEFAULT_TARGET)
    parser.add_argument('--source-root', type=Path, default=SOURCE_ROOT)
    args = parser.parse_args(argv)
    if not args.execute:
        print(json.dumps({'complete': False, 'executed': False, 'reason': 'explicit --execute required'}))
        return 2
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    try:
        receipt = prepare(args)
    except BaseException as error:
        receipt = {'complete': False, 'executed': False,
                   'error': str(error) if isinstance(error, PreparationError) else type(error).__name__}
    print(json.dumps(receipt, sort_keys=True))
    return 0 if receipt.get('complete') else 1


if __name__ == '__main__':
    raise SystemExit(main())

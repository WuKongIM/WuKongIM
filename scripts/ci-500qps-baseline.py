#!/usr/bin/env python3
"""Reuse only a recent successful main qualification with identical server inputs."""
import datetime
import json
import os
import re
import subprocess
import urllib.request

WORKFLOW = 'three-node-chat-lifecycle-regression.yml'
REPO = 'WuKongIM/WuKongIM'
REQUIRED_JOBS = {
    'Nightly direct 500 QPS qualification',
    'PR 500 QPS / channel-append',
    'PR 500 QPS / mixed-send',
    'PR 500 QPS / tcp-sendack',
}


def unrelated(path):
    # The exercised server paths do not load public docs or embedded Demo assets.
    # All Go, dependencies, configuration, scripts and workflow inputs stay bound.
    return path in {'CHANGELOG.md', 'README.md', 'README_EN.md'} or path.startswith((
        'docs/', 'docs-site/', 'demo/chatdemo/', 'internal/access/api/demoui/dist/',
    ))


def eligible_run(run, now):
    sha = run.get('head_sha', '')
    try:
        age = now - datetime.datetime.fromisoformat(run['created_at'].replace('Z', '+00:00'))
    except (KeyError, ValueError, TypeError):
        return False
    return (run.get('head_branch') == 'main'
            and run.get('event') in {'schedule', 'workflow_dispatch'}
            and run.get('path') == '.github/workflows/' + WORKFLOW
            and type(run.get('id')) is int and run['id'] > 0
            and re.fullmatch('[0-9a-f]{40}', sha) is not None
            and datetime.timedelta(0) <= age <= datetime.timedelta(days=7))


def complete_jobs(document, run):
    jobs = document.get('jobs', [])
    # Never accept a truncated page or duplicate/conflicting names.
    if document.get('total_count') != len(jobs) or len(jobs) > 100:
        return False
    for name in REQUIRED_JOBS:
        matches = [job for job in jobs if job.get('name') == name]
        if len(matches) != 1:
            return False
        job = matches[0]
        if any(job.get(key) != value for key, value in {
                'status': 'completed', 'conclusion': 'success', 'run_attempt': 1,
                'run_id': run['id'], 'head_sha': run['head_sha']}.items()):
            return False
    return True


def command(*args):
    return subprocess.check_output(args, stderr=subprocess.DEVNULL, timeout=15)


def changed_paths(before, after):
    if before == after:
        return []
    output = command('git', 'diff', '--name-only', '--no-renames', '-z', before, after, '--')
    return [path for path in output.decode().split('\0') if path]


def choose(base, head, runs, now, ancestor, diff, jobs):
    """Require three distinct clean main runs; never bypass a newer matching failure."""
    if not all(re.fullmatch('[0-9a-f]{40}', sha or '') for sha in (base, head)):
        return {'reuse': False, 'reason': 'invalid_source_identity'}
    paths = [p for p in diff(base, head) if p]
    if not paths or not all(unrelated(p) for p in paths):
        return {'reuse': False, 'reason': 'performance_inputs_changed'}
    verified = []
    source_sha = None
    for run in runs[:20]:
        if not eligible_run(run, now):
            return {'reuse': False, 'reason': 'main_metadata_not_eligible'}
        if not ancestor(run['head_sha'], base):
            continue
        if any(p and not unrelated(p) for p in diff(run['head_sha'], head)):
            continue
        # A newer matching failed, retried or incomplete run cannot be hidden
        # by searching farther back for a green one.
        if (run.get('status') != 'completed' or run.get('conclusion') != 'success'
                or run.get('run_attempt') != 1 or not complete_jobs(jobs(run['id']), run)):
            return {'reuse': False, 'reason': 'matching_main_baseline_not_clean'}
        if run['id'] in verified:
            return {'reuse': False, 'reason': 'duplicate_baseline_identity'}
        if not verified:
            source_sha = run['head_sha']
        verified.append(run['id'])
        if len(verified) == 3:
            return {'reuse': True, 'reason': 'identical_inputs_verified_main',
                    'source_sha': source_sha, 'run_id': verified[0],
                    'verified_runs': verified, 'candidate_sha': head}
    return {'reuse': False, 'reason': 'no_verified_matching_main_baseline'}


def main():
    def api(path):
        request = urllib.request.Request('https://api.github.com/repos/' + REPO + '/actions/' + path,
                                         headers={'Accept': 'application/vnd.github+json',
                                                  'User-Agent': 'wukongim-ci-baseline'})
        # Public metadata only: no token or artifact access is needed. Rate
        # limits or unavailable evidence require fresh tests, never a skip.
        with urllib.request.urlopen(request, timeout=10) as response:
            body = response.read(2 * 1024 * 1024 + 1)
        if len(body) > 2 * 1024 * 1024:
            raise ValueError('metadata exceeds bound')
        return json.loads(body)

    def ancestor(before, after):
        return subprocess.run(['git', 'merge-base', '--is-ancestor', before, after],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15).returncode == 0

    # Every unavailable or malformed fact falls back to fresh performance jobs.
    result = {'reuse': False, 'reason': 'baseline_evidence_unavailable'}
    try:
        head = command('git', 'rev-parse', 'HEAD').decode().strip()
        if command('git', 'status', '--porcelain').strip():
            raise ValueError('dirty candidate')
        result = choose(os.environ.get('BASE_SHA', ''), head,
                        api('workflows/' + WORKFLOW + '/runs?branch=main&per_page=20').get('workflow_runs', []),
                        datetime.datetime.now(datetime.timezone.utc), ancestor, changed_paths,
                        lambda run: api('runs/' + str(run) + '/jobs?filter=latest&per_page=100'))
    except (OSError, subprocess.SubprocessError, ValueError, TypeError, KeyError, AttributeError):
        pass
    print(json.dumps(result, sort_keys=True))
    with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as output:
        output.write('reuse=' + str(result['reuse']).lower() + '\n')
    with open(os.environ['GITHUB_STEP_SUMMARY'], 'a', encoding='utf-8') as summary:
        if result['reuse']:
            summary.write('500 QPS performance baseline reused: https://github.com/' + REPO +
                          '/actions/runs/' + str(result['run_id']) + '\n\nIdentical server inputs at `' +
                          result['source_sha'] + '`; three clean runs: ' + str(result['verified_runs']) +
                          '. Correctness and unit/race still execute.\n')
        else:
            summary.write('Fresh 500 QPS performance checks required: ' + result['reason'] + '.\n')


if __name__ == '__main__':
    main()

#!/usr/bin/env python3
"""Aggregate approved safe fields from one frozen historical self-chat CI report.

This is a report transform, not a product replay or fresh acceptance test.
Raw identities, payloads, message IDs/sequences and arbitrary strings are not
emitted. The sole retained string is the explicitly approved fixed policy enum.
"""
import argparse
import hashlib
import json
from pathlib import Path

EXPECTED_BYTES = 47382
EXPECTED_SHA256 = '4c73a24706aa702213183925f3fda8a13142c8b7b0b70bf8f04ddf4505d1a7c1'


def extract(path):
    raw = path.read_bytes()
    if len(raw) != EXPECTED_BYTES or hashlib.sha256(raw).hexdigest() != EXPECTED_SHA256:
        raise ValueError('original report differs from the frozen source')
    report = json.loads(raw)
    if report['token_authentication'] != 'production_default':
        raise ValueError('unexpected fixed authentication-policy enum')
    for key in ('hash_slots', 'suppression_window_ms'):
        if type(report[key]) is not int or report[key] < 0:
            raise ValueError('unexpected top-level count')
    if type(report['passed']) is not bool or len(report['cases']) != 2:
        raise ValueError('unexpected frozen case structure')
    cases = []
    for case in report['cases']:
        for key in ('nodes', 'whole_cluster_restarts'):
            if type(case[key]) is not int or case[key] < 0:
                raise ValueError('unexpected fixed case count')
        for key in ('passed', 'invalid_auth_refused'):
            if type(case[key]) is not bool:
                raise ValueError('unexpected fixed case outcome')
        cases.append({
            'nodes': case['nodes'],
            'passed': case['passed'],
            'invalid_auth_refused': case['invalid_auth_refused'],
            'whole_cluster_restarts': case['whole_cluster_restarts'],
            'acknowledged_record_count': len(case['acknowledged']),
            'receive_record_count': len(case['receives']),
            'history_observation_count': len(case['history']),
            'history_recorded_message_count': sum(len(row['messages']) for row in case['history']),
        })
    return {
        'schema': 'wukongim.safe_self_chat_ci_summary/v1',
        'source_report_sha256': EXPECTED_SHA256,
        'source_report_bytes': EXPECTED_BYTES,
        'mode': 'historical_ci_report_transform',
        'fresh_product_execution': False,
        'passed': report['passed'],
        'hash_slots': report['hash_slots'],
        'token_authentication': 'production_default',
        'suppression_window_ms': report['suppression_window_ms'],
        'cases': cases,
        'algorithm': 'Verify the exact original report byte count and SHA-256; retain only the approved root and case booleans, integer counts and fixed production_default authentication enum; count ACK/RECV/history records without copying message contents or identities.',
        'excluded_fields': ['arbitrary_strings', 'payload', 'uid', 'from_uid', 'channel_id', 'client_msg_no', 'message_id', 'message_idstr', 'message_seq'],
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--original-report', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.write_text(json.dumps(extract(args.original_report), indent=2) + '\n')


if __name__ == '__main__':
    main()

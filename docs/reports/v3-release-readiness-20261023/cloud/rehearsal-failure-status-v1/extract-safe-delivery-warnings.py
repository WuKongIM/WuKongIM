#!/usr/bin/env python3
"""Extract a finite public delivery-warning summary from three original local logs.

The original raw logs must remain outside this public archive. This parser
verifies their frozen hashes before reading, selects only plan-incomplete
warnings, and emits no raw UID, channel, ClientMsgNo, payload or error text.
Anonymous recipient ordinals are assigned by first occurrence in the explicit
source-file order; they are not user IDs or workload roster indexes.
"""
import argparse
import hashlib
import json
import re
from pathlib import Path

EXPECTED = {'terminal/evidence/logs/service-1.log': {'sha256': 'b5ae4349cba8138d9b81bdb513bb54acc9f10bacb169993c81427dd7a17bcc6b', 'bytes': 42202}, 'terminal/evidence/logs/service-2.log': {'sha256': 'a4ccf6f31da3742eab15b00f12f5f0439810b26ca7601f779bf6212875b47807', 'bytes': 41659}, 'terminal/evidence/logs/service-3.log': {'sha256': '33dbf8ea1122d0a34aad6abe13893b19b6c90c1f9ee1c191b860854babb5d0b8', 'bytes': 41203}}


def extract(source):
    rows = []
    recipient_ordinals = {}
    inputs = []
    for relative, expected in EXPECTED.items():
        path = source / relative
        raw = path.read_bytes()
        digest = hashlib.sha256(raw).hexdigest()
        if len(raw) != expected['bytes'] or digest != expected['sha256']:
            raise ValueError('source bytes differ from the frozen public reference')
        inputs.append({'path': relative, 'bytes': len(raw), 'sha256': digest})
        for line_number, line in enumerate(raw.decode('utf-8').splitlines(), 1):
            if 'delivery.plan_incomplete' not in line:
                continue
            body = json.loads(line[line.index('{'):])
            if body['event'] != 'internal.app.delivery.plan_incomplete':
                raise ValueError('unexpected event')
            if body['phase'] not in ('presence', 'owner_push') or body['result'] != 'timeout' or body['mode'] != 'durable':
                raise ValueError('unexpected bounded warning category')
            uid = body['uid']
            if not isinstance(uid, str) or not uid:
                raise ValueError('missing recipient sample identity')
            if uid not in recipient_ordinals:
                recipient_ordinals[uid] = len(recipient_ordinals) + 1
            timestamp = re.search(r'\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}', line)
            node = re.search(r'-service-([123]) wukongim\[', line)
            if not timestamp or not node or not isinstance(body['recipients'], int):
                raise ValueError('unexpected structured log prefix')
            rows.append({
                'source_path': relative,
                'source_line': line_number,
                'product_timestamp_asia_shanghai': timestamp.group(0),
                'embedded_host_service_node': int(node.group(1)),
                'result': 'timeout',
                'phase': body['phase'],
                'mode': 'durable',
                'recipient_count': body['recipients'],
                'recipient_sample_ordinal': recipient_ordinals[uid],
                'error_category': 'context_deadline_exceeded' if body.get('error') == 'context deadline exceeded' else 'other_error_text_omitted',
            })
    if len(rows) != 37:
        raise ValueError('unexpected warning count')
    return {
        'schema': 'wukongim.safe_delivery_warning_summary/v1',
        'source_manifest_sha256': '50fba8e114bf93182734305a312808bc8c840981bd3ba03e869834cd482f7def',
        'source_inputs': inputs,
        'algorithm': 'Verify exactly three original SHA-256/size pairs; enumerate files in EXPECTED order and lines in original order; select exact plan-incomplete event; retain fixed timestamp/node/timeout/phase/mode/recipient-count fields; replace raw recipient UID with first-occurrence ordinal; reduce error text to a fixed category.',
        'identity_fields_omitted': ['uid', 'recipient_uid', 'channel_id', 'client_msg_no', 'clientmsgno', 'payload', 'arbitrary_error_text'],
        'recipient_sample_ordinal_is_workload_index': False,
        'warning_count': len(rows),
        'presence_warning_count': sum(r['phase'] == 'presence' for r in rows),
        'owner_push_warning_count': sum(r['phase'] == 'owner_push' for r in rows),
        'causal_message_join_established': False,
        'warnings': rows,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--original-source-root', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    result = extract(args.original_source_root)
    args.output.write_text(json.dumps(result, indent=2) + '\n')


if __name__ == '__main__':
    main()

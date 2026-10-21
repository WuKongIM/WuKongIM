"""Independent offline closure probes; no real credentials or provider sockets."""
import copy
import hashlib
import importlib.util
import io
import json
import logging
import os
from pathlib import Path
import socket
import tempfile
from unittest import mock

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-provider')
HERE = Path(__file__).resolve().parent

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

p = load('spec_v2_provider', ROOT/'scripts/aliyun-object-storage-provider.py')
t = load('spec_v2_wire', ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py')

def client(wire, resource=None):
    return p.Provider(resource=resource or t.RESOURCE, access_key_id='FIXTURE-AK',
                      access_key_secret=t.SECRET, security_token=t.TOKEN, http_adapter=wire)

def writes(wire):
    return sum(r[0].method == 'PUT' or r[2] in ('BatchSetCdnDomainConfig','RefreshObjectCaches') for r in wire.requests)

def rejected(call):
    try:
        call()
    except p.ProviderError:
        return True
    return False

result = {'source_sha256': hashlib.sha256((ROOT/'scripts/aliyun-object-storage-provider.py').read_bytes()).hexdigest(),
          'network_allowed': False, 'configuration': [], 'logging': []}
with mock.patch.object(socket.socket, 'connect', side_effect=AssertionError('provider network forbidden')):
    for mode in ('original-unmanaged', 'original-tampered-body', 'tampered-rehashed',
                 'other-domain-rehashed', 'other-resource-rehashed', 'unknown-row-hidden-rehashed',
                 'incomplete-observation', 'extra-field', 'same-resource-managed-positive'):
        w = t.Wire()
        w.configs.append(t.config(p.FUNCTION, {'source_url': r'^/repo\z' if mode.endswith('positive') else r'^/old\z',
                                             'target_url': '/public/old', 'flag': 'break', 'sequence': '1'}, 90))
        if mode == 'unknown-row-hidden-rehashed':
            w.configs[-1] = t.config('gzip', {'enable': 'on'}, 90)
        c = client(w)
        try:
            before = c.read_configuration()
            if mode in ('original-tampered-body','tampered-rehashed','unknown-row-hidden-rehashed'):
                before['configs'] = before['configs'][:-1]
            if mode == 'other-domain-rehashed':
                before['domain'] = 'other.invalid'
            if mode == 'other-resource-rehashed':
                before['resource_sha256'] = '0'*64
            if mode.endswith('rehashed'):
                before['sha256'] = p.digest({k:v for k,v in before.items() if k != 'sha256'})
            if mode == 'incomplete-observation':
                before.pop('resource_sha256')
            if mode == 'extra-field':
                before['approved'] = True
            w.requests.clear()
            receipt = None
            try:
                receipt = c.apply_rewrites([t.route()], before=before,
                                          managed_ids=[90] if mode.endswith('positive') else [])
                denied = False
            except p.ProviderError:
                denied = True
            row = dict(mode=mode, rejected=denied, write_calls=writes(w),
                       state=receipt.get('state') if receipt else None)
            if mode.endswith('positive'):
                row['exact_managed_id'] = json.loads(next(r[3]['Functions'][0] for r in w.requests
                                                         if r[2]=='BatchSetCdnDomainConfig'))[0]['configId']
                assert not denied and row['write_calls']==1 and row['state']=='readback_verified' and row['exact_managed_id']=='90'
            else:
                assert denied and row['write_calls']==0, row
            result['configuration'].append(row)
        finally:
            c.close()

    # Preserve the original late-guard scenario and test constructor/RPC/OSS entry seams.
    w=t.Wire(); c=client(w); w.objects[t.KEY]=(t.DATA,t.FACTS['cache_control'])
    before=c.read_configuration()
    core=logging.getLogger('aliyunsdkcore.client'); parent=logging.getLogger('aliyunsdkcore'); root=logging.getLogger()
    loggers=(core,parent,root)
    prior=[(x.level,x.propagate,x.disabled,tuple(x.handlers)) for x in loggers]
    stream=io.StringIO(); handler=logging.StreamHandler(stream); core.addHandler(handler)
    try:
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'payload'; path.write_bytes(t.DATA)
            operations=[('constructor',lambda:client(w)), ('original-observe-resource',c.observe_resource),
                        ('read-configuration',c.read_configuration), ('read-object',lambda:c.read_object(t.KEY,t.FACTS)),
                        ('put',lambda:c.put_create_only(t.KEY,path,t.FACTS)),
                        ('apply',lambda:c.apply_rewrites([t.route()],before=before,managed_ids=[])),
                        ('refresh',lambda:c.refresh(['https://packages.githubim.com/repo'],max_polls=1,poll_interval=0))]
            for mode in ('core','inherited-parent','inherited-root','environment'):
                core.setLevel(logging.NOTSET); parent.setLevel(logging.NOTSET); root.setLevel(logging.WARNING)
                core.propagate=False
                if mode=='core': core.setLevel(logging.DEBUG)
                if mode=='inherited-parent': parent.setLevel(logging.DEBUG)
                if mode=='inherited-root': root.setLevel(logging.DEBUG)
                with mock.patch.dict(os.environ, {'DEBUG':'sdk' if mode=='environment' else ''}):
                    snapshot=[(x.level,x.propagate,x.disabled,tuple(x.handlers)) for x in loggers]
                    environment=dict(os.environ)
                    for name,call in operations:
                        stream.seek(0); stream.truncate(); w.requests.clear()
                        denied=rejected(call)
                        raw=stream.getvalue()
                        state_unchanged=snapshot==[(x.level,x.propagate,x.disabled,tuple(x.handlers)) for x in loggers] and environment==dict(os.environ)
                        row=dict(mode=mode,operation=name,rejected=denied,adapter_calls=len(w.requests),
                                 credential_logged=any(v in raw for v in (t.TOKEN,t.SECRET,'FIXTURE-AK')),global_state_unchanged=state_unchanged)
                        result['logging'].append(row)
                        assert denied and not w.requests and not row['credential_logged'] and state_unchanged, row
    finally:
        core.removeHandler(handler)
        for logger,(level,propagate,disabled,handlers) in zip(loggers,prior):
            logger.setLevel(level); logger.propagate=propagate; logger.disabled=disabled
            assert tuple(logger.handlers)==handlers
        c.close()

(HERE/'probe.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'configuration_cases':len(result['configuration']),'logging_cases':len(result['logging']),
                  'all_passed':True,'network_allowed':False}))

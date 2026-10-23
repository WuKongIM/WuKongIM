"""One approved derived profile; stdout is only the closed public projection."""
import hashlib,json,os,pathlib,sys
import trace_offline as A

def main():
    path,node,kind,coverage,expected,private_path=sys.argv[1:]
    path=pathlib.Path(path);node=int(node);private_path=pathlib.Path(private_path)
    A.verify_tool(path,expected)
    meta,records=A.decode_trace_profile(path)
    if meta["file_sha256"]!=expected:raise A.OfflineError("input_changed")
    public=A.aggregate_trace_profile(meta,records,node_id=node,profile_type=kind,coverage=coverage)
    private_path=A._private_target(private_path)
    fd=os.open(private_path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    written=0
    with os.fdopen(fd,'wb') as stream:
        for chunk in json.JSONEncoder(separators=(',',':')).iterencode({'metadata':meta,'records':records}):
            raw=chunk.encode()
            if len(raw)>8388608-written:raise A.OfflineError('decode_limit')
            if stream.write(raw)!=len(raw):raise A.OfflineError('io_failure')
            written+=len(raw)
    encoded=(json.dumps(public,sort_keys=True)+'\n').encode()
    if len(encoded)>65536:raise A.OfflineError('projection_invalid')
    sys.stdout.buffer.write(encoded)

if __name__=='__main__':
    try:main()
    except A.OfflineError as error:
        print(json.dumps({'status':'partial','error_class':str(error)}));raise SystemExit(1)
    except Exception:
        print('{"status":"partial","error_class":"unknown"}');raise SystemExit(1)

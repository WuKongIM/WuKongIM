"""Synthetic stdlib pprof optional-string defaults; no real profile input."""
import gzip,pathlib,tempfile,unittest
import trace_offline as A
from synthetic_profile import field,profile

def omitted_function_fields(*,omit=(2,4),missing_id=False):
    out=[]
    for n,w,v in A.fields(gzip.decompress(profile())):
        if n==5:
            fn=b''.join(field(k,value) for k,_,value in A.fields(v) if k not in omit and not (missing_id and k==1))
            out.append(field(5,fn))
        else:out.append(field(n,v))
    return gzip.compress(b''.join(out),mtime=0)
class OptionalProtoStrings(unittest.TestCase):
    def test_omitted_name_filename_default_to_empty_string(self):
        with tempfile.TemporaryDirectory() as d:
            path=pathlib.Path(d)/'synthetic.pb.gz'
            for omit in [(2,),(4,),(2,4)]:
                with self.subTest(omit=omit):
                    path.write_bytes(omitted_function_fields(omit=omit));meta,records=A.decode_trace_profile(path)
                    if 2 in omit:self.assertEqual(records[0]['frames'][0]['function'],'')
                    if 4 in omit:self.assertEqual(records[0]['frames'][0]['file'],'')
                    self.assertEqual(records[0]['values'],[2,17])
    def test_missing_function_id_still_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            path=pathlib.Path(d)/'synthetic.pb.gz';path.write_bytes(omitted_function_fields(missing_id=True))
            with self.assertRaises(A.OfflineError):A.decode_trace_profile(path)
if __name__=='__main__':unittest.main()

"""Evidence-only finite grouping estimate; not the provider implementation."""
from pathlib import Path
import json,re,math,collections,hashlib
base=Path(__file__).parent

def escaped_literal(text):
    output='';index=0
    while index<len(text):
        if text[index]=='\\':index+=1;assert index<len(text)
        output+=text[index];index+=1
    assert re.escape(output)==text
    return output

def inverse(rule):
    source=rule['source_url'];target=rule['target_url']
    assert source.startswith('^') and source.endswith(r'\z')
    inner=source[1:-2]
    if '(' not in inner:
        assert '$' not in target
        return {escaped_literal(inner):target}
    prefix,tail=inner.split('(',1);assert tail.endswith(')') and tail.count(')')==1
    prefix=escaped_literal(prefix);assert target.endswith('$1') and target.count('$')==1
    pairs={}
    for item in tail[:-1].split('|'):
        suffix=escaped_literal(item);assert suffix and prefix+suffix not in pairs
        pairs[prefix+suffix]=target[:-2]+suffix
    return pairs

summary={}
for label in ('A','B'):
    delivery=json.loads((base/'first-run'/label/'delivery.json').read_bytes());routes=delivery['routes']
    groups={};single=[]
    for number,row in enumerate(routes):
        uri=row['public_uri'];target='/'+row['object_key']
        if uri=='/' or row['object_key'].startswith('generated/'):
            single.append((number,row));continue
        if target==uri:
            match=re.match(r'^/snapshots/(?:preview|stable)/r[0-9]+-[a-f0-9]{64}/',uri);assert match
            prefix=match.group();kind='snapshot-identity';q=prefix
        elif row['object_key']=='public'+uri:
            prefix='/';q='/public/';kind='public-alias'
        else:
            suffix=uri[1:];assert target.endswith(suffix)
            prefix='/';q=target[:-len(suffix)];kind='current-entrypoint'
            assert re.fullmatch(r'/snapshots/(?:preview|stable)/r[0-9]+-[a-f0-9]{64}/site/',q)
        suffix=uri[len(prefix):];assert suffix and target==q+suffix
        groups.setdefault((kind,prefix,q),[]).append((number,suffix,row))
    candidates=[];distribution=[]
    for (kind,prefix,q),entries in sorted(groups.items(),key=lambda pair:min(x[0] for x in pair[1])):
        entries=sorted(entries,key=lambda x:x[1]);chunks=[];chunk=[]
        def expr(chunk):return '^'+re.escape(prefix)+'('+'|'.join(re.escape(x[1]) for x in chunk)+r')\z'
        for entry in entries:
            assert len(expr([entry]).encode())<=1024
            if chunk and len(expr(chunk+[entry]).encode())>1024:chunks.append(chunk);chunk=[]
            chunk.append(entry)
        if chunk:chunks.append(chunk)
        overhead=len(('^'+re.escape(prefix)+'('+r')\z').encode())
        total=sum(len(re.escape(x[1]).encode())+1 for x in entries)
        # Capacity is 1024-overhead+1 because final alternative has no separator.
        lower=math.ceil(total/(1024-overhead+1))
        distribution.append({'kind':kind,'source_prefix':prefix,'target_prefix':q,'routes':len(entries),'escaped_suffix_plus_separator_bytes':total,'fixed_group_size_lower_bound':lower,'lexicographic_next_fit_rules':len(chunks)})
        for chunk in chunks:
            candidates.append({'first_original_route':min(x[0] for x in chunk),'source_url':expr(chunk),'target_url':q+'$1','flag':'break','members':len(chunk)})
    for number,row in single:
        candidates.append({'first_original_route':number,'source_url':'^'+re.escape(row['public_uri'])+r'\z','target_url':'/'+row['object_key'],'flag':'break','members':1})
    candidates.sort(key=lambda x:(x['first_original_route'],x['source_url']))
    expected={x['public_uri']:'/'+x['object_key'] for x in routes};actual={}
    for sequence,c in enumerate(candidates,1):
        c['sequence']=str(sequence);c['expression_bytes']=len(c['source_url'].encode());assert c['expression_bytes']<=1024
        for uri,target in inverse(c).items():
            assert uri not in actual
            actual[uri]=target
    assert actual==expected
    regexes=[re.compile(c['source_url'].replace(r'\z',r'\Z')) for c in candidates]
    adjacent=0
    for uri,target in expected.items():
        matches=[(c,rx.fullmatch(uri)) for c,rx in zip(candidates,regexes) if rx.fullmatch(uri)]
        assert len(matches)==1
        c,m=matches[0];mapped=c['target_url'][:-2]+m.group(1) if c['target_url'].endswith('$1') else c['target_url'];assert mapped==target
        for other in (uri+'x',uri+'/',uri+'\n',uri+'?x=1',uri+'#x',uri.replace('/','//',1),uri.replace('/','/%2f',1),uri+'%00',uri+'/../x'):
            if other in expected:continue
            assert not any(rx.fullmatch(other) for rx in regexes),other;adjacent+=1
    obj={x['object_key']:x for x in delivery['objects']}
    for row in routes:
        item=obj[actual[row['public_uri']][1:]]
        assert all(row[k]==item[k] for k in ('sha256','size','cache_control'))
    data={'analysis_only':True,'provider_implemented':False,'live_pcre_verified':False,'source_routes_sha256':hashlib.sha256((base/'first-run'/label/'routes.json').read_bytes()).hexdigest(),'rules':candidates}
    (base/('candidate-rules-'+label+'.json')).write_text(json.dumps(data,indent=2)+'\n')
    summary[label]={'routes':len(routes),'objects':len(obj),'kind_counts':dict(collections.Counter(x['kind'] for x in routes)),'groups':distribution,'literal_exceptions':[x[1]['public_uri'] for x in single],'candidate_rules':len(candidates),'fixed_partition_size_lower_bound':sum(g['fixed_group_size_lower_bound'] for g in distribution)+len(single),'maximum_expression_bytes':max(c['expression_bytes'] for c in candidates),'finite_inverse_exact_set_and_target_equality':True,'all_route_object_byte_cache_facts_bound':True,'neighbor_nonmatches':adjacent,'python_regex_only_not_live_pcre':True}
(base/'route-analysis.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps(summary,indent=2))

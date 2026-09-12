#!/usr/bin/env python3
"""Exercise the built human control CLI using a temporary synthetic data root．"""
from __future__ import annotations
import argparse
import copy
import hashlib
import hmac
import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

ROOT=Path(__file__).resolve().parents[1]

def encode(value): return json.dumps(value,ensure_ascii=False,separators=(',',':'),sort_keys=True).encode()
def sign(value,key):
    result=copy.deepcopy(value)
    result['auth'].pop('mac',None)
    result['auth']['mac']=hmac.new(key,encode(result),hashlib.sha256).hexdigest()
    return encode(result)
def load(path): return json.loads(path.read_bytes())

def smoke():
    with tempfile.TemporaryDirectory(prefix='karte-c2-smoke-') as temporary:
        root=Path(temporary)
        data=root/'data';data.mkdir(mode=0o700)
        config=root/'private/registrations'
        credential=root/'private/runtime/producer.json'
        binary=root/('karte-ephy-control.exe' if os.name=='nt' else 'karte-ephy-control')
        subprocess.run(['go','build','-o',str(binary),'./cmd/karte-ephy-control'],cwd=ROOT,check=True)
        def control(operation,*args):
            command=[str(binary),'-data-root',str(data),'-config-root',str(config),*map(str,args),operation]
            return json.loads(subprocess.check_output(command,cwd=ROOT))
        grant=load(ROOT/'schemas/karte-ephy/v2/fixtures/grant.json')
        grant['valid_from']='2000-01-01T00:00:00Z'
        grant_path=root/'grant.json';grant_path.write_bytes(encode(grant))
        control('configure','-grant',grant_path,'-producer-credential',credential)
        key=bytes.fromhex(load(credential)['key'])
        proposal=load(ROOT/'schemas/karte-ephy/v2/fixtures/conversation-user-final.proposal.json')
        raw=sign(proposal,key)
        def publish(namespace,identifier,raw):
            path=data/namespace/(identifier+'.json');path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(raw);path.chmod(0o600)
        pending='.mdsys/ephy/outbox/v2/pending'
        requests='.mdsys/context/v2/requests'
        publish(pending,proposal['candidate_id'],raw)
        assert control('process')['processed']==1
        receipt_path=data/'.mdsys/ephy/outbox/v2/receipts'/(proposal['candidate_id']+'.json')
        receipt=load(receipt_path)
        documents=list((data/'content').rglob('*.md'));assert len(documents)==1
        canonical=documents[0].read_bytes();digest=hashlib.sha256(canonical).hexdigest()
        assert digest==receipt['applied']['sha256'] and receipt['applied']['revision']==1
        publish(pending,proposal['candidate_id'],raw)
        assert control('process')['processed']==1
        assert documents[0].read_bytes()==canonical
        altered=copy.deepcopy(proposal);altered['candidate_id']=str(uuid.uuid4());altered['events'][0]['text']='同じ event ID の異なる合成本文．'
        publish(pending,altered['candidate_id'],sign(altered,key))
        assert control('process')['failed']==1
        rejection=load(data/'.mdsys/ephy/outbox/v2/rejected'/(altered['candidate_id']+'.result.json'))
        assert rejection['code']=='id_reuse' and documents[0].read_bytes()==canonical
        request=load(ROOT/'schemas/karte-context/v2/fixtures/read-request.json')
        request['target']=receipt['applied'];request['request_id']=str(uuid.uuid4())
        query=sign(request,key);publish(requests,request['request_id'],query)
        assert control('process')['processed']==1
        response_path=data/'.mdsys/context/v2/responses'/(request['request_id']+'.json')
        response=load(response_path)
        assert response['status']=='ok' and response['results'][0]['markdown'].encode()==canonical
        grant['policy_revision']+=1;grant['consent_epoch']+=1;grant['enabled']=False
        grant_path.write_bytes(encode(grant));control('configure','-grant',grant_path,'-producer-credential',credential)
        assert not response_path.exists()
        publish(requests,request['request_id'],query);control('process')
        denied=load(response_path);assert denied['status']=='not_available' and denied['results']==[]
        publish(pending,proposal['candidate_id'],raw);assert control('process')['failed']==1
        assert load(receipt_path)==receipt and documents[0].read_bytes()==canonical
        return {'status':'PASS','canonical_documents':1,'canonical_revision':1,'canonical_sha256':digest,'same_candidate_replay':'single_effect','changed_event_payload':'id_reuse','read_back':'exact_bytes','revocation':'write_blocked_and_response_purged','data':'temporary_synthetic_only'}

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--report',type=Path);args=parser.parse_args()
    report=smoke();raw=json.dumps(report,ensure_ascii=False,indent=2)+'\n'
    if args.report:args.report.write_text(raw)
    print(raw,end='')
if __name__=='__main__':main()

#!/usr/bin/env python3
"""Generate the standalone v2 schemas owned by Karte (no v1 mutation)."""
from __future__ import annotations
import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
def obj(fields, optional=()):
    return {"type":"object","additionalProperties":False,"properties":fields,"required":[k for k in fields if k not in optional]}
def arr(item, lo=0, hi=256, unique=False):
    schema={"type":"array","items":item,"minItems":lo,"maxItems":hi}
    if unique: schema["uniqueItems"]=True
    return schema
def enum(*values): return {"enum":list(values)}
def ref(name): return {"$ref":"#/$defs/"+name}
def nullable(value): return {"anyOf":[value,{"type":"null"}]}
ID={"type":"string","format":"uuid","pattern":"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$","not":{"const":"00000000-0000-0000-0000-000000000000"}}
TOKEN={"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$"}
PROJECT={"type":"string","pattern":"^[a-z0-9][a-z0-9._-]{0,63}$"}
HASH={"type":"string","pattern":"^[0-9a-f]{64}$"}
INT={"type":"integer","minimum":1,"maximum":9007199254740991}
TEXT={"type":"string","maxLength":65536,"description":"Maximum 65536 UTF-8 bytes；Karte also checks bytes after decoding，without normalization．"}
DATE={"type":"string","format":"date"}
TIME={"type":"string","format":"date-time"}
ZONE={"type":"string","minLength":1,"maxLength":128,"description":"An installed IANA timezone，validated by Karte．"}
SENS=enum("public","internal","confidential","restricted")
RECORD_TYPE=enum("conversation","summary","ephy_diary")
MUTATION=enum("create_record","append_events","revise_derivation")
D={}
D['auth']=obj({'key_id':TOKEN,'mac':HASH})
D['actor']=obj({'type':{'const':'ephy'},'id':TOKEN})
D['target']=obj({'doc_id':ID,'revision':INT,'sha256':HASH})
D['event-ref']=obj({'event_id':ID,'event_revision':INT})
D['source-ref']=obj({'doc_id':ID,'revision':INT,'sha256':HASH,'conversation_id':ID,'turn_ids':arr(ID,1,256,True),'events':arr(ref('event-ref'),1,256,True)})
D['asr']=obj({'provider':TOKEN,'model_revision':TOKEN,'final_revision':INT})
D['speech-unit']=obj({'unit_id':TOKEN,'state':enum('started','completed','interrupted','unknown')})
D['assistant']=obj({'generation':enum('completed','canceled','failed'),'display':enum('confirmed_full','confirmed_prefix','none'),'playback':enum('completed','interrupted','failed','unknown','not_started'),'speech_units':arr(ref('speech-unit'))})
D['event']=obj({'conversation_id':ID,'scope_id':ID,'producer_instance_id':ID,'event_id':ID,'event_seq':INT,'event_revision':{'const':1},'turn_id':ID,'event_type':enum('user_final','assistant_result','correction'),'input_kind':enum('text','asr_final'),'text':TEXT,'occurred_at':TIME,'timezone':ZONE,'local_date':DATE,'asr':ref('asr'),'assistant':ref('assistant'),'consent_epoch':INT,'corrects':ref('event-ref'),'correction_reason':enum('asr_error','user_content')},optional=('input_kind','asr','assistant','corrects','correction_reason'))
D['event']['allOf']=[
 {'if':{'properties':{'event_type':{'const':'user_final'}}},'then':{'required':['input_kind'],'not':{'anyOf':[{'required':['assistant']},{'required':['corrects']},{'required':['correction_reason']}]}}},
 {'if':{'properties':{'event_type':{'const':'user_final'},'input_kind':{'const':'asr_final'}},'required':['input_kind']},'then':{'required':['asr']}},
 {'if':{'properties':{'event_type':{'const':'assistant_result'}}},'then':{'required':['assistant'],'not':{'anyOf':[{'required':['asr']},{'required':['input_kind']},{'required':['corrects']},{'required':['correction_reason']}]}}},
 {'if':{'properties':{'event_type':{'const':'correction'}}},'then':{'required':['corrects','correction_reason'],'not':{'anyOf':[{'required':['asr']},{'required':['assistant']},{'required':['input_kind']}]}}}
]
D['claim']=obj({'class':enum('observed_utterance','user_report','ephy_interpretation'),'text':{**TEXT,'minLength':1},'source_refs':arr(ref('source-ref'),1,64)})
D['derivation']=obj({'kind':enum('summary','ephy_diary'),'input_refs':arr(ref('source-ref'),1,64),'model_id':TOKEN,'model_revision':TOKEN,'template_id':TOKEN,'template_revision':TOKEN,'generated_at':TIME,'job_id':ID,'claims':arr(ref('claim'),1,256)})
D['record-spec']=obj({'record_type':RECORD_TYPE,'title':{'type':'string','minLength':1,'maxLength':512,'pattern':'^[^\\r\\n\\u0000]+$'},'conversation_id':ID,'segment_no':INT,'timezone':ZONE,'local_date':DATE},optional=('conversation_id','segment_no'))
D['record-spec']['allOf']=[
 {'if':{'properties':{'record_type':{'const':'conversation'}}},'then':{'required':['conversation_id','segment_no']}},
 {'if':{'properties':{'record_type':{'const':'summary'}}},'then':{'required':['conversation_id'],'not':{'required':['segment_no']}}},
 {'if':{'properties':{'record_type':{'const':'ephy_diary'}}},'then':{'not':{'anyOf':[{'required':['conversation_id']},{'required':['segment_no']}]}}}
]
D['adoption']=obj({'mode':{'const':'policy'},'actor_id':TOKEN,'policy_id':ID,'policy_revision':INT,'decision':{'const':'scope_allowed'}})
D['derivation-descriptor']=copy.deepcopy(D['derivation']);D['derivation-descriptor']['properties']['claims']={'type':'null'}
D['record-meta']=obj({'schema_version':{'const':'2.0'},'scope_id':ID,'producer_instance_id':ID,'logical_record_key':{'type':'string','minLength':1,'maxLength':512},'revision':INT,'record':ref('record-spec'),'adoption':ref('adoption'),'human_edited':{'type':'boolean'},'derivation':ref('derivation-descriptor')},optional=('derivation',))
D['record']=obj({'doc_id':ID,'project':PROJECT,'kind':enum('note','journal'),'sensitivity':SENS,'tags':arr(TOKEN,0,64,True),'provenance_types':arr(TOKEN,1,64,True),'authorship':{'const':'ephy'},'runtime_record':ref('record-meta'),'events':arr(ref('event'),1,256),'derivation':ref('derivation')},optional=('events','derivation'))
D['record']['oneOf']=[{'required':['events'],'not':{'required':['derivation']}},{'required':['derivation'],'not':{'required':['events']}}]
D['classification']=obj({'kind':enum('note','journal'),'sensitivity':SENS,'tags':arr(TOKEN,0,64,True),'provenance_types':arr(TOKEN,1,64,True)})
D['grant']=obj({'schema_version':{'const':'2.0'},'policy_id':ID,'policy_revision':INT,'enabled':{'type':'boolean'},'user_id':TOKEN,'actor_id':TOKEN,'producer_instance_id':ID,'scope_id':ID,'storage_area_id':TOKEN,'project':PROJECT,'records':{'type':'object','minProperties':1,'maxProperties':3,'additionalProperties':False,'properties':{k:ref('classification') for k in ('conversation','summary','ephy_diary')}},'operations':arr(MUTATION,1,3,True),'denied_tags':arr(TOKEN,0,64,True),'valid_from':TIME,'expires_at':TIME,'consent_epoch':INT,'scope_generation':INT,'storage':{'const':True},'training':{'const':False},'external_transfer':{'const':False},'key_id':TOKEN},optional=('expires_at',))
COMMON={'scope_id':ID,'producer_instance_id':ID,'actor':ref('actor'),'policy_id':ID,'policy_revision':INT,'consent_epoch':INT,'scope_generation':INT}
D['proposal']=obj({'schema_version':{'const':'2.0'},'candidate_id':ID,'operation':MUTATION,'logical_record_key':{'type':'string','minLength':1,'maxLength':512},**COMMON,'record':ref('record-spec'),'target':nullable(ref('target')),'events':arr(ref('event'),1,1),'derivation':ref('derivation'),'created_at':TIME,'auth':ref('auth')},optional=('events','derivation'))
D['proposal']['allOf']=[
 {'if':{'properties':{'operation':{'const':'create_record'}}},'then':{'properties':{'target':{'type':'null'}}},'else':{'properties':{'target':ref('target')}}},
 {'if':{'properties':{'record':{'properties':{'record_type':{'const':'conversation'}}}}},'then':{'required':['events'],'not':{'required':['derivation']},'properties':{'operation':enum('create_record','append_events')}},'else':{'required':['derivation'],'not':{'required':['events']},'properties':{'operation':enum('create_record','revise_derivation')}}}
]
D['receipt']=obj({'schema_version':{'const':'2.0'},'candidate_id':ID,'proposal_hash':HASH,'status':enum('accepted','already_applied'),'applied':ref('target'),'applied_event_ids':arr(ID,0,1),'adoption':ref('adoption')})
D['rejection']=obj({'schema_version':{'const':'2.0'},'candidate_id':ID,'status':{'const':'rejected'},'code':TOKEN})
D['query']=obj({'text':{'type':'string','maxLength':2048},'record_types':arr(RECORD_TYPE,1,3,True),'timezone':ZONE,'date_from':DATE,'date_to':DATE,'limit':{'type':'integer','minimum':1,'maximum':100}},optional=('timezone','date_from','date_to'))
D['request']=obj({'protocol_version':{'const':'2.0'},'request_id':ID,'operation':enum('search','read'),**COMMON,'created_at':TIME,'auth':ref('auth'),'query':nullable(ref('query')),'target':nullable(ref('target'))})
D['request']['oneOf']=[{'properties':{'operation':{'const':'search'},'query':ref('query'),'target':{'type':'null'}}},{'properties':{'operation':{'const':'read'},'target':ref('target'),'query':{'type':'null'}}}]
D['read-result']=obj({'target':ref('target'),'record':ref('record-spec'),'scope_id':ID,'state':enum('active','stale'),'source_refs':arr(ref('source-ref'),0,64),'markdown':{'type':'string'},'events':arr(ref('event'),1,256),'derivation':ref('derivation')},optional=('markdown','events','derivation'))
D['response']=obj({'protocol_version':{'const':'2.0'},'request_id':ID,'status':TOKEN,'results':arr(ref('read-result'),0,100)})
D['policy-version']=obj({'scope_id':ID,'policy_id':ID,'policy_revision':INT,'consent_epoch':INT,'scope_generation':INT})
D['capabilities']=obj({'protocol_version':{'const':'2.0'},'record_schema':{'const':'2.0'},'operations':arr(enum('create_record','append_events','revise_derivation','search','read'),5,5,True),'enabled':{'type':'boolean'},'policies':arr(ref('policy-version'),0,256)})

def references(value):
    if isinstance(value,dict):
        if '$ref' in value: yield value['$ref'].removeprefix('#/$defs/')
        for child in value.values(): yield from references(child)
    elif isinstance(value,list):
        for child in value: yield from references(child)

def generate():
    schemas={
      'karte-ephy/v2/proposal.schema.json':'proposal',
      'karte-ephy/v2/receipt.schema.json':'receipt',
      'karte-ephy/v2/rejection.schema.json':'rejection',
      'karte-ephy/v2/record.schema.json':'record',
      'karte-ephy/v2/source-reference.schema.json':'source-ref',
      'karte-ephy/v2/grant.schema.json':'grant',
      'karte-context/v2/request.schema.json':'request',
      'karte-context/v2/response.schema.json':'response',
      'karte-context/v2/capabilities.schema.json':'capabilities',
    }
    for name,kind in schemas.items():
        needed=set(); queue=list(references(D[kind]))
        while queue:
            key=queue.pop()
            if key in needed: continue
            needed.add(key); queue.extend(references(D[key]))
        schema={'$schema':'https://json-schema.org/draft/2020-12/schema','$id':'https://raw.githubusercontent.com/kirimine170/Karte/main/schemas/'+name,'title':'Karte Runtime records v2 '+kind,**D[kind]}
        if needed:schema['$defs']={k:D[k] for k in sorted(needed)}
        path=ROOT/'schemas'/name;path.parent.mkdir(parents=True,exist_ok=True)
        path.write_text(json.dumps(schema,ensure_ascii=False,indent=2)+'\n')
if __name__=='__main__': generate()

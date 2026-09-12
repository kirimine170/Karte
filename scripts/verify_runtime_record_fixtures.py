#!/usr/bin/env python3
"""Validate Karte-owned v2 schemas，MAC bytes and semantic fixtures．

This is a contract oracle for synthetic data，not a Runtime canonical writer．
The 0x42 key is public test material and must never be registered for real data．
"""
from __future__ import annotations
import argparse
import hashlib
import hmac
import json
from pathlib import Path
import uuid
from jsonschema import Draft202012Validator, FormatChecker

TEST_KEY = bytes([0x42]) * 32

def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result: raise ValueError('duplicate_key')
        result[key] = value
    return result

def _integer(raw):
    value = int(raw)
    if raw == '-0' or abs(value) > 9007199254740991: raise ValueError('integer_required')
    return value

def _reject_float(_): raise ValueError('integer_required')

def _unicode(value, depth=0):
    if depth > 64: raise ValueError('json_depth')
    if isinstance(value, float) or isinstance(value, int) and abs(value) > 9007199254740991: raise ValueError('integer_required')
    if isinstance(value, str): value.encode('utf-8', errors='strict')
    elif isinstance(value, dict):
        for key, item in value.items(): key.encode('utf-8', errors='strict'); _unicode(item, depth + 1)
    elif isinstance(value, list):
        for item in value: _unicode(item, depth + 1)

def strict_loads(raw):
    if isinstance(raw, bytes): raw = raw.decode('utf-8', errors='strict')
    if len(raw.encode('utf-8')) > 2 << 20: raise ValueError('payload_capacity')
    value = json.loads(raw, object_pairs_hook=_pairs, parse_int=_integer, parse_float=_reject_float, parse_constant=_reject_float)
    _unicode(value)
    return value

def canonical(value):
    _unicode(value)
    return json.dumps(value, ensure_ascii=False, separators=(',', ':'), sort_keys=True).encode('utf-8')

def signing_bytes(payload):
    value = json.loads(json.dumps(payload))
    del value['auth']['mac']
    return canonical(value)

def proposal_hash(payload): return hashlib.sha256(signing_bytes(payload)).hexdigest()

def document_id(payload):
    name = 'karte-record-v2\0' + payload['scope_id'] + '\0' + payload['producer_instance_id'] + '\0' + payload['logical_record_key']
    return str(uuid.uuid5(uuid.NAMESPACE_URL, name))

def verify(root):
    root = Path(root)
    mutation = root / 'schemas/karte-ephy/v2'
    context = root / 'schemas/karte-context/v2'
    errors = []
    def check(condition, label):
        if not condition: errors.append(label)
    def load(path): return strict_loads(path.read_bytes())
    def validate(path, schema):
        payload = load(path)
        validator = Draft202012Validator(load(schema), format_checker=FormatChecker())
        for error in validator.iter_errors(payload):
            errors.append(f'{path.name}: schema mismatch at {list(error.absolute_path)} ({error.validator})')
        return payload
    for directory in (mutation, context):
        for path in directory.glob('*.schema.json'): Draft202012Validator.check_schema(load(path))
    for path in (mutation/'fixtures').glob('*.proposal.json'):
        p = validate(path, mutation/'proposal.schema.json')
        check(hmac.compare_digest(hmac.new(TEST_KEY, signing_bytes(p), hashlib.sha256).hexdigest(), p['auth']['mac']), path.name+': MAC')
    for path in (mutation/'fixtures').glob('*.receipt.json'): validate(path, mutation/'receipt.schema.json')
    for path in (mutation/'fixtures').glob('*.record.json'): validate(path, mutation/'record.schema.json')
    validate(mutation/'fixtures/grant.json', mutation/'grant.schema.json')
    for name in ('read','search'):
        request = validate(context/f'fixtures/{name}-request.json', context/'request.schema.json')
        check(hmac.compare_digest(hmac.new(TEST_KEY, signing_bytes(request), hashlib.sha256).hexdigest(), request['auth']['mac']), name+': request MAC')
        validate(context/f'fixtures/{name}-response.json', context/'response.schema.json')
    validate(context/'fixtures/capabilities.json', context/'capabilities.schema.json')

    first = load(mutation/'fixtures/conversation-user-final.proposal.json')
    receipt = load(mutation/'fixtures/conversation-user-final.receipt.json')
    record = load(mutation/'fixtures/conversation-user-final.record.json')
    stored = load(mutation/'fixtures/conversation-user-final.canonical.json')
    event_bytes = (mutation/'fixtures/conversation-user-final.event.canonical.json').read_bytes()
    check(event_bytes == canonical(first['events'][0]), 'exact canonical event bytes')
    check(hashlib.sha256(event_bytes).hexdigest() == '01065eadd62df6a0af52210def3c81f633017119375175799a5a29e56f491f46', 'Step 0 event digest')
    check(hashlib.sha256(stored['markdown'].encode()).hexdigest() == stored['sha256'] == receipt['applied']['sha256'], 'canonical Markdown digest')
    check(receipt['proposal_hash'] == proposal_hash(first), 'proposal digest')
    check(receipt['applied']['doc_id'] == record['doc_id'] == document_id(first), 'stable document identity')
    check(record['events'] == first['events'], 'accepted event preservation')

    scenario = load(mutation/'fixtures/conversation-8-messages.scenario.json')
    event_hashes = {}; candidate_hashes = {}; versions = []
    proposal_validator = Draft202012Validator(load(mutation/'proposal.schema.json'), format_checker=FormatChecker())
    for p, receipt in zip(scenario['proposals'], scenario['receipts'], strict=True):
        check(proposal_validator.is_valid(p), 'scenario proposal schema')
        event = p['events'][0]
        event_hashes[event['event_id']] = hashlib.sha256(canonical(event)).hexdigest()
        candidate_hashes[p['candidate_id']] = proposal_hash(p)
        versions.append(receipt['applied']['revision'])
        check(receipt['proposal_hash'] == proposal_hash(p), 'scenario proposal hash')
        check(receipt['applied']['doc_id'] == document_id(first), 'same conversation identity')
        check(receipt['applied_event_ids'] == [event['event_id']], 'receipt applied event identity')
    check(versions == list(range(1,9)), 'monotonic revisions')
    check(len(event_hashes) == scenario['expected_event_count'] == len(scenario['record']['events']) == 8, 'complete eight-event record')
    duplicate = scenario['duplicate_event']; duplicate_receipt = scenario['duplicate_receipt']
    event = duplicate['events'][0]
    check(event_hashes[event['event_id']] == hashlib.sha256(canonical(event)).hexdigest(), 'same-event retransmission')
    check(duplicate_receipt['status'] == 'already_applied' and duplicate_receipt['applied'] == scenario['receipts'][-1]['applied'], 'single existing effect')
    reused = scenario['reused_id']
    check(hashlib.sha256(canonical(reused['events'][0])).hexdigest() != event_hashes[event['event_id']] and scenario['reused_result'] == 'id_reuse', 'ID-reuse semantic result')
    check(proposal_hash(reused) != proposal_hash(duplicate), 'changed payload has a new hash')

    diary = load(mutation/'fixtures/derived-diary.proposal.json')
    d = diary['derivation']
    check({c['class'] for c in d['claims']} == {'user_report','ephy_interpretation'}, 'reported claim and AI interpretation separation')
    check(all(c['source_refs'] == d['input_refs'] for c in d['claims']), 'claim source ranges')
    source = d['input_refs'][0]
    check(source['doc_id'] == record['doc_id'] and source['sha256'] == stored['sha256'] and source['revision'] == 1, 'versioned derivation source')
    check(source['turn_ids'] == [first['events'][0]['turn_id']] and source['events'][0]['event_revision'] == 1, 'turn and event revision source')
    interruption = load(mutation/'fixtures/interrupted-answer.record.json')['events'][-1]['assistant']
    check(interruption['generation'] == 'canceled' and interruption['playback'] == 'interrupted', 'interruption status')
    check(interruption['speech_units'][0]['state'] == 'completed' and interruption['speech_units'][1]['state'] == 'started', 'unobserved completion is not invented')
    raw_text = load(mutation/'fixtures/raw-text.proposal.json')['events'][0]['text']
    parsed_text = load(mutation/'fixtures/raw-text.record.json')['events'][0]['text']
    check(raw_text == parsed_text, 'raw text escaping round trip')
    cases = load(mutation/'fixtures/strict-json.cases.json')
    for case in cases:
        try: actual = canonical(strict_loads(case['raw'])); accepted = True
        except (ValueError, UnicodeError): actual = None; accepted = False
        check(accepted == case['valid'], 'strict JSON: '+case['name'])
        if accepted: check(actual.decode() == case['canonical'], 'canonical bytes: '+case['name'])
    return errors

def main(argv=None):
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[1])
    args=parser.parse_args(argv)
    errors=verify(args.root)
    for error in errors: print(error)
    if not errors: print('Karte v2 schemas，canonical bytes，MAC and semantic fixtures: PASS')
    return bool(errors)
if __name__=='__main__': raise SystemExit(main())

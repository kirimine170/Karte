import json
from pathlib import Path

from jsonschema import Draft202012Validator


directory = Path(__file__).resolve().parents[1] / "schemas" / "hybrid-sharing" / "v0.1"
schema = json.loads((directory / "manifest.schema.json").read_text(encoding="utf-8"))
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema)
validator.validate(json.loads((directory / "fixtures" / "valid.json").read_text(encoding="utf-8")))
for path in sorted((directory / "fixtures").glob("invalid-*.json")):
    if validator.is_valid(json.loads(path.read_text(encoding="utf-8"))):
        raise SystemExit(f"{path.name} unexpectedly passed schema validation")
compatibility_fixture = Path(__file__).resolve().parents[1] / "testdata" / "hybrid-sharing" / "id-compatibility.json"
compatibility = json.loads(compatibility_fixture.read_text(encoding="utf-8"))
validator.validate(compatibility["manifest"])
for case in compatibility["cases"]:
    manifest = dict(compatibility["manifest"], root_resource_id=case["doc_id"])
    if validator.is_valid(manifest) != case["portable"]:
        raise SystemExit(f"{case['name']}: unexpected manifest acceptance")
print("Portable package manifest and legacy identity fixtures passed")

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
print("Portable package manifest fixtures passed")

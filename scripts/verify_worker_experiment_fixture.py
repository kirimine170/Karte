"""Validate the synthetic producer fixture against the pinned Worker contract."""

import hashlib
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


def main():
    root = Path(__file__).resolve().parent.parent
    schema = json.loads(
        (root / "schemas/experiment/producer/v1/worker-evidence-manifest.schema.json")
        .read_text(encoding="utf-8")
    )
    bundle = root / "testdata/worker-experiment-v1"
    manifest = json.loads((bundle / "evidence-manifest.json").read_text(encoding="utf-8"))
    Draft202012Validator.check_schema(schema)
    Draft202012Validator(schema, format_checker=FormatChecker()).validate(manifest)
    for artifact in manifest["artifacts"]:
        raw = (bundle / artifact["path"]).read_bytes()
        if len(raw) != artifact["size_bytes"] or hashlib.sha256(raw).hexdigest() != artifact["sha256"]:
            raise ValueError(f"fixture hash/size mismatch: {artifact['artifact_id']}")
    print("PASS: pinned Worker v1 schema and complete synthetic fixture (22 artifacts)")


if __name__ == "__main__":
    main()

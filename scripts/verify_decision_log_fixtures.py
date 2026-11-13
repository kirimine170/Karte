#!/usr/bin/env python3
"""Validate decision log fixtures against the published record schema."""

import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


def main():
    root = Path(__file__).resolve().parents[1] / "schemas" / "decision-log" / "v0.1"
    schema = json.loads((root / "record.schema.json").read_bytes())
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema, format_checker=FormatChecker())
    fixtures = sorted((root / "fixtures").glob("*.json"))
    if not fixtures:
        raise SystemExit("No decision log fixtures found")
    for fixture in fixtures:
        record = json.loads(fixture.read_bytes())
        # Draft 2020-12 cannot compare two instance values. The Go fixture
        # test rejects the self-reference separately.
        expected_valid = (
            not fixture.name.startswith("invalid-")
            or fixture.name == "invalid-self-replacement.json"
        )
        if validator.is_valid(record) != expected_valid:
            # Validation failures should not print private decision contents.
            raise SystemExit(f"{fixture.name}: unexpected decision schema validation result")
    print(f"Validated {len(fixtures)} decision log fixtures")


if __name__ == "__main__":
    main()

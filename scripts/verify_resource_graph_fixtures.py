"""Validate G0 fixture shape. Go tests validate the cross-record invariants."""
import json
from pathlib import Path

from jsonschema import Draft202012Validator


def unique_keys(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=unique_keys)


def main():
    root = Path(__file__).resolve().parents[1] / "schemas" / "hybrid-sharing" / "v0.1"
    schema = read_json(root / "resource-graph.schema.json")
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema)
    fixtures = sorted((root / "resource-graph-fixtures").glob("*.json"))
    if not fixtures:
        raise SystemExit("no fixtures found")
    for path in fixtures:
        if not path.name.startswith(("valid-", "invalid-schema-", "invalid-domain-")):
            raise SystemExit(f"unknown fixture kind: {path.name}")
        error = next(validator.iter_errors(read_json(path)), None)
        if path.name.startswith("invalid-schema-"):
            if error is None:
                raise SystemExit(f"{path.name}: expected schema rejection")
        elif error is not None:
            raise SystemExit(f"{path.name}: {error.message}")
    print(f"{len(fixtures)} Resource Graph fixture shapes verified")

    compatibility = read_json(root.parents[2] / "testdata" / "hybrid-sharing" / "id-compatibility.json")
    if validator.is_valid(compatibility["manifest"]):
        raise SystemExit("legacy portable manifest unexpectedly accepted as a G0 record")
    for index, case in enumerate(compatibility["cases"]):
        graph = {
            "type": "resource_graph", "schema_version": "0.1", "workspace_id": "fixture-workspace",
            "resources": [{
                "resource_id": f"local-{index}", "kind": "markdown", "legacy_doc_id": case["doc_id"],
            }],
            "revisions": [], "relations": [], "change_events": [],
        }
        if validator.is_valid(graph) != case["graph_alias"]:
            raise SystemExit(f"{case['name']}: unexpected G0 alias acceptance")
    print("Resource Graph and portable manifest identity boundaries verified")


if __name__ == "__main__":
    main()

"""Exercise the real Strata MCP hub against a temporary Karte root, without a model.

python smoke_worklog.py --strata-root E:/Strata --binary PATH/karte-worklog.exe
No Strata configuration or running service is changed.
"""
import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--strata-root", type=Path, required=True)
    parser.add_argument("--binary", type=Path, required=True)
    args = parser.parse_args()
    sys.path.insert(0, str(args.strata_root.resolve()))
    from serve.mcp import McpHub

    with tempfile.TemporaryDirectory(prefix="karte-strata-") as root:
        subprocess.run([str(args.binary.resolve()), "--root", root, "init"], check=True, capture_output=True)
        hub = McpHub({"karte_worklog": {"command": str(args.binary.resolve()), "args": ["--root", root, "serve"]}})
        try:
            hub.start(wait=True)
            names = {t["name"] for t in hub.template_tools()}
            assert "karte_worklog__karte_work_start" in names, names

            def call(tool, arguments):
                result = hub.call("karte_worklog__" + tool, arguments)
                assert result["ok"], result
                return json.loads(result["text"])["value"]

            first = call("karte_work_start", {"task_id": "strata-smoke", "event_id": "start", "title": "Strata検索失敗", "objective": "MCP経由の失敗記録と検索を確かめる"})
            assert first["recorded"] and first["views_ready"]
            event = {"task_id": "strata-smoke", "event_id": "failure", "kind": "failure", "text": "検索の429エラーを申告", "next_action": "再現条件と待機時間を調べる", "evidence": [{"reference": "test:synthetic-429"}]}
            failure = call("karte_work_append", event)
            assert failure["sequence"] == 2
            duplicate = call("karte_work_append", event)
            assert duplicate["duplicate"] and duplicate["sequence"] == 2
            found = call("karte_work_search", {"query": "429"})
            assert found["results"][0]["task_id"] == "strata-smoke"
            record = call("karte_work_read", {"task_id": "strata-smoke"})
            assert record["events"][1]["origin"] == "model_report"
            day = record["events"][0]["recorded_at"]
            from datetime import datetime, timezone, timedelta
            date = datetime.fromisoformat(day.replace("Z", "+00:00")).astimezone(timezone(timedelta(hours=9))).date().isoformat()
            daily = call("karte_work_daily", {"date": date})
            assert "429" in daily["body"]
            denied = hub.call("karte_worklog__karte_work_append", {**event, "event_id": "spoof", "origin": "host_observation"})
            assert not denied["ok"]
            print("PASS: Strata hub → Karte start, failure, duplicate, search, read, daily, source boundary")
        finally:
            hub.close()


if __name__ == "__main__":
    main()

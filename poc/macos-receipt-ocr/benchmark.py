#!/usr/bin/env python3
import argparse
import json
import pathlib
import statistics
import subprocess

EXPECTED = ["架空商店", "渋谷店", "2026/08/13", "12:34", "158", "128", "98", "384", "30", "414", "1,000", "586"]


def percentile(values, fraction):
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, max(0, int(len(ordered) * fraction + 0.999999) - 1))]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--fixtures", required=True)
    parser.add_argument("--iterations", type=int, default=10)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    fixtures = sorted(pathlib.Path(args.fixtures).glob("*.png"))
    if not fixtures:
        raise SystemExit("fixtureがありません")

    records = []
    for compute in ("auto", "cpu"):
        for fixture in fixtures:
            for iteration in range(args.iterations):
                command = [args.binary, "--image", str(fixture), "--compute", compute]
                record = json.loads(subprocess.check_output(command, text=True))
                text = "\n".join(line["text"] for line in record["lines"])
                record["fixture"] = fixture.stem
                record["iteration"] = iteration + 1
                record["fieldRecall"] = sum(token in text for token in EXPECTED) / len(EXPECTED)
                records.append(record)

    summaries = []
    for compute in ("auto", "cpu"):
        for fixture in fixtures:
            group = [r for r in records if r["compute"] == compute and r["fixture"] == fixture.stem]
            latencies = [r["elapsedMs"] for r in group]
            summaries.append({
                "compute": compute,
                "fixture": fixture.stem,
                "iterations": len(group),
                "p50Ms": statistics.median(latencies),
                "p95Ms": percentile(latencies, 0.95),
                "meanCPUTimeMs": statistics.mean(r["userCPUMs"] + r["systemCPUMs"] for r in group),
                "meanResidentDeltaMiB": statistics.mean(r["residentDeltaMiB"] for r in group),
                "maxResidentAfterMiB": max(r["residentAfterMiB"] for r in group),
                "fieldRecall": statistics.mean(r["fieldRecall"] for r in group),
            })

    pathlib.Path(args.output).write_text(
        json.dumps({"expectedFields": EXPECTED, "summaries": summaries, "runs": records}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()

# Karte resource budget contract

This contract defines a reproducible PR gate for resource-shape regressions．It intentionally separates deterministic limits from host-dependent observation．The authoritative，reviewed limits live in `resource-budget/baseline.json`；CI never rewrites that file．

## Gate scope

The PR gate exercises four fixed scenarios．Each scenario performs two warmup runs followed by five measured runs，and compares the median．Backend scenarios run with `GOMAXPROCS=2` and restore the caller value afterward．The standard-library `testing.AllocsPerRun` helper serializes only its focused allocation submeasurement；scheduler，queue，goroutine，byte，and latency samples use the fixed two-P setting．A monotonic clock is required for every latency sample．

| Scenario | Backend fixture | Frontend fixture | Gating signals |
| --- | --- | --- | --- |
| `idle` | Start and stop the four-worker job manager without jobs | — | live workers，active and retained goroutine deltas |
| `continuous_input` | Submit 10，000 replace-pending jobs behind one running blocker | Dispatch 10，000 editor input events through the real debounce path | operations，peak queue，goroutines，allocated bytes，pending timers，preview calls，DOM nodes，broad median latency |
| `pdf_100` | — | Load a deterministic 100-page PDF proxy，enter scroll mode，then jump to page 50 | 100 slots，at most eight live canvases，bounded page requests and DOM nodes，broad median latency |
| `graph_1000` | Deep-copy a cached graph with 1，000 tagged nodes and 999 edges | Render 1，000 nodes and 999 edges through the real SVG DOM path with D3 physics mocked | node and edge counts，allocations，allocated bytes，DOM and canvas counts，broad median latency |
| `concurrent_heavy` | Run one synthetic ASR-category and one synthetic LLM-category job while one more of each remains pending | — | running and pending counts，fixed work units，goroutine bound，broad median latency |

The synthetic heavy fixture verifies scheduler concurrency and category bounds only．It does not claim production ASR or local-LLM performance coverage．There is currently no production local-LLM callsite，so adding a production LLM category or model budget is blocked until that implementation and a pinned model contract exist．

Exact counts and queue bounds fail on any change．Allocation and latency limits use deliberately broad ceilings so a material regression fails without turning normal shared-runner variance into noise．The jsdom graph fixture mocks force-simulation execution；it measures deterministic DOM construction，not physics CPU cost．

## Versioned baseline and reports

Schema version 1 has one sampling policy shared by the baseline and every report．The only implemented statistic and aggregation are `median`．Metric units are explicit：

| Unit | Meaning | Normal direction |
| --- | --- | --- |
| `count` | live objects，workers，goroutines，DOM nodes，or canvases | `eq` for a contract，otherwise `lte` |
| `operations` | submitted，requested，or completed fixture operations | `eq` for fixture completeness，otherwise `lte` |
| `allocations` | Go allocations per operation | `lte` |
| `bytes` | Go allocated bytes for the measured operation window | `lte` |
| `milliseconds` | monotonic elapsed duration | `lte` |

`resourcegate` rejects an unknown schema field or version，an unsafe identifier，an unknown，missing，or duplicate metric，a source，unit，statistic，or policy mismatch，a non-finite value，and a sample-count mismatch．Every measurement must contain exactly `policy.samples` samples，and `value` must exactly equal their computed median；callers cannot provide an independent summary value．Baseline statistics are restricted to the implemented `median` set．Metric，scenario，suite，unit，and observe-profile identifiers use a strict lowercase identifier alphabet，which also keeps the generated Markdown table inert．

`comparison` defines direction：`lte` means smaller is acceptable，`gte` means larger is acceptable，`eq` is an exact contract，and `observe` never gates．A gating metric is always required．Raw report samples are written before the limit comparison，so a threshold failure still leaves evidence for the CI artifact．

## Observe-only measurements

The baseline declares three non-gating profiles：

- `native_idle_process` records process RSS and CPU from a release desktop artifact．Allocator，OS，desktop runtime，and runner load make these unsuitable for the deterministic PR gate．
- `native_asr_llm` records RSS，CPU，and latency only after ASR and a future local LLM have pinned runtime and model digests．Native model loading and hardware acceleration are outside the current fixture，and the LLM callsite is not implemented．
- `browser_pdf_graph` records browser heap，paint，and GPU memory from pinned Chromium traces．jsdom has no layout，raster，paint，or GPU pipeline．

The exact warmup，sample count，and capture procedures are stored beside each profile in the baseline．Observe-only data may be attached to a PR or release audit，but absence or variance does not change the deterministic gate．OS-specific native smoke jobs should remain separate from this workflow．

## Local reproduction

Run the two measurement producers，then compare their raw reports：

```sh
mkdir -p /tmp/karte-resource-budget frontend/dist
touch frontend/dist/.placeholder
KARTE_RESOURCE_REPORT=/tmp/karte-resource-budget/backend.json go test . -run '^TestResourceBudgetGate$' -count=1
(cd frontend && KARTE_RESOURCE_REPORT=/tmp/karte-resource-budget/frontend.json npm test -- --run src/__tests__/resource-budget.test.ts)
go run ./cmd/resourcegate \
  --baseline resource-budget/baseline.json \
  --report /tmp/karte-resource-budget/backend.json \
  --report /tmp/karte-resource-budget/frontend.json \
  --markdown-out /tmp/karte-resource-budget/summary.md \
  --json-out /tmp/karte-resource-budget/evaluation.json
```

For stability auditing，run the focused backend gate with `-count=10` and run the focused Vitest file ten times．Each individual run already contains two warmups and five samples；do not merge samples across independent reports．

## Reviewing a threshold change

There is no baseline-update command or CI write-back．A threshold change is an explicit source diff：

1. Reproduce the focused tests from a clean checkout and retain all raw reports．
2. Confirm whether the change is a fixture-contract change，a resource regression，or expected implementation cost．Do not raise a ceiling to hide a flaky native or browser measurement；move such a signal to an observe-only profile．
3. Update the metric `baseline` to the reviewed median and change `limit` only with a written reason．Keep exact contract limits exact．Do not edit report `value` or samples into the baseline．
4. Review `resource-budget/baseline.json` as a normal code diff，including schema version，unit，direction，source，description，and stability class．
5. Re-run the backend gate ten times，the frontend gate ten times，the strict schema tests，and `resourcegate` against one backend and one frontend raw report．

The Resource Budget workflow posts the comparison table to the GitHub step summary and uploads backend samples，frontend samples，the merged evaluation，and the summary even when the comparison fails．A missing report is itself a gate failure．

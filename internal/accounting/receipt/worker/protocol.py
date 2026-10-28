from __future__ import annotations

import json
import sys
from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class RecognizeRequest:
    request_id: str
    path: str
    output_dir: str
    visualize: bool
    document_type: str
    region: tuple[float, float, float, float] | None
    preprocess_variant: str


def parse_request(line: str) -> RecognizeRequest:
    payload = json.loads(line)
    if payload.get("method") != "recognize":
        raise ValueError("unsupported method")
    request_id = payload.get("request_id")
    params = payload.get("params") or {}
    if not isinstance(request_id, str) or not request_id:
        raise ValueError("request_id is required")
    if not isinstance(params.get("path"), str) or not params["path"]:
        raise ValueError("params.path is required")
    if not isinstance(params.get("output_dir"), str) or not params["output_dir"]:
        raise ValueError("params.output_dir is required")
    raw_region = params.get("region")
    region = None
    if raw_region is not None:
        if isinstance(raw_region, dict):
            raw_region = [raw_region.get("x"), raw_region.get("y"), raw_region.get("width"), raw_region.get("height")]
        if not isinstance(raw_region, list) or len(raw_region) != 4:
            raise ValueError("params.region must contain x,y,width,height")
        region = tuple(float(value) for value in raw_region)
        if any(value < 0 or value > 1 for value in region) or region[2] <= 0 or region[3] <= 0:
            raise ValueError("params.region must be normalized and non-empty")
    return RecognizeRequest(
        request_id=request_id,
        path=params["path"],
        output_dir=params["output_dir"],
        visualize=bool(params.get("visualize", True)),
        document_type=str(params.get("document_type", "receipt")),
        region=region,
        preprocess_variant=str(params.get("preprocess_variant", "")),
    )


def emit(payload: dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def progress(request_id: str, stage: str, message: str, value: float | None = None) -> None:
    payload: dict[str, Any] = {
        "type": "progress",
        "request_id": request_id,
        "stage": stage,
        "message": message,
    }
    if value is not None:
        payload["progress"] = value
    emit(payload)


def error(request_id: str, code: str, message: str, retryable: bool) -> None:
    emit({
        "type": "error",
        "request_id": request_id,
        "error": {"code": code, "message": message, "retryable": retryable},
    })

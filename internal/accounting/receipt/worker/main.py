from __future__ import annotations

import logging
import os
import sys
import traceback

from analyzer import ReceiptAnalyzer, UnsupportedInputError
from protocol import emit, error, parse_request, progress


logging.basicConfig(stream=sys.stderr, level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("karte-receipt-worker")


def main() -> int:
    try:
        analyzer = ReceiptAnalyzer()
    except ModuleNotFoundError as exc:
        emit({"type": "fatal_error", "code": "MODEL_NOT_FOUND", "message": f"YomiToku 0.13.1を導入してください: {exc}"})
        return 2
    except Exception as exc:
        logger.exception("model initialization failed")
        emit({"type": "fatal_error", "code": "MODEL_INITIALIZATION_FAILED", "message": str(exc)})
        return 2

    emit({
        "type": "ready",
        "protocol_version": "1",
        "engine": {"name": "mock" if analyzer.mock else "yomitoku", "version": analyzer.engine_version},
        "device": analyzer.device,
        "mode": analyzer.mode,
    })

    for line in sys.stdin:
        if not line.strip():
            continue
        request_id = "unknown"
        try:
            request = parse_request(line)
            request_id = request.request_id
            result = analyzer.recognize(
                request.path,
                request.output_dir,
                request.visualize,
                lambda stage, message: progress(request.request_id, stage, message),
                request.region,
                request.preprocess_variant,
            )
            emit({"type": "result", "request_id": request.request_id, "result": result})
        except FileNotFoundError as exc:
            error(request_id, "FILE_NOT_FOUND", str(exc), False)
        except UnsupportedInputError as exc:
            error(request_id, "UNSUPPORTED_INPUT", str(exc), False)
        except ValueError as exc:
            error(request_id, "PROTOCOL_ERROR", str(exc), False)
        except KeyboardInterrupt:
            error(request_id, "CANCELLED", "cancelled", True)
        except Exception as exc:
            logger.error("inference failed: %s\n%s", exc, traceback.format_exc())
            code = "OUT_OF_MEMORY" if "memory" in str(exc).lower() else "INFERENCE_FAILED"
            error(request_id, code, str(exc), True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

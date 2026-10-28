from __future__ import annotations

import json
import os
import shutil
import time
from pathlib import Path
from typing import Any, Callable


class UnsupportedInputError(Exception):
    pass


class ReceiptAnalyzer:
    def __init__(self) -> None:
        self.mock = os.getenv("RECEIPT_OCR_MOCK") == "1"
        self.mode = os.getenv("RECEIPT_OCR_MODE", "lite")
        self.requested_device = os.getenv("RECEIPT_OCR_DEVICE", "mps")
        self.device = "mock" if self.mock else self._select_device(self.requested_device)
        self.engine_version = "0.13.1"
        self._analyzer: Any = None
        if not self.mock:
            self._analyzer = self._create_analyzer(self.device)

    @staticmethod
    def _select_device(requested: str) -> str:
        import torch

        if requested == "cpu":
            return "cpu"
        if requested == "mps" and torch.backends.mps.is_available():
            return "mps"
        return "cpu"

    def _create_analyzer(self, device: str) -> Any:
        from yomitoku.document_analyzer import DocumentAnalyzer

        configs: dict[str, Any] = {
            "ocr": {"text_detector": {}, "text_recognizer": {}},
            "layout_analyzer": {"layout_parser": {}, "table_structure_recognizer": {}},
        }
        if self.mode == "lite":
            configs["ocr"]["text_recognizer"].update({
                "model_name": "parseq-tiny-dynw-v4",
                "dynamic_width": True,
                "batch_bucketing": True,
                "source_downscale": True,
            })
            if device == "cpu":
                configs["ocr"]["text_detector"]["infer_onnx"] = True
                configs["ocr"]["text_recognizer"]["num_parallel_batches"] = 4
        return DocumentAnalyzer(configs=configs, device=device, visualize=True)

    def recognize(
        self,
        input_path: str,
        output_dir: str,
        visualize: bool,
        on_stage: Callable[[str, str], None],
        region: tuple[float, float, float, float] | None = None,
        preprocess_variant: str = "",
    ) -> dict[str, Any]:
        path = Path(input_path)
        if not path.is_file():
            raise FileNotFoundError(input_path)
        if path.suffix.lower() not in {".jpg", ".jpeg", ".png", ".webp", ".pdf"}:
            raise UnsupportedInputError("unsupported file type")
        Path(output_dir).mkdir(parents=True, exist_ok=True)
        started = time.perf_counter()
        on_stage("recognizing", "文字列とレイアウトを認識しています")

        if self.mock:
            pages, visualization = self._mock_result(path, Path(output_dir), visualize)
        else:
            try:
                pages, visualization = self._real_result(path, Path(output_dir), visualize, region, preprocess_variant)
            except Exception:
                if self.device != "mps":
                    raise
                on_stage("recognizing", "MPS推論に失敗したためCPU軽量モードで再試行します")
                self.device = "cpu"
                self.mode = "lite"
                self._analyzer = self._create_analyzer("cpu")
                pages, visualization = self._real_result(path, Path(output_dir), visualize, region, preprocess_variant)

        on_stage("serializing", "OCR結果を共通形式へ変換しています")
        raw_path = Path(output_dir) / "yomitoku.json"
        raw_path.write_text(json.dumps({"pages": pages}, ensure_ascii=False, indent=2), encoding="utf-8")
        return {
            "engine": "mock" if self.mock else "yomitoku",
            "engine_version": self.engine_version,
            "device": self.device,
            "mode": self.mode,
            "duration_ms": int((time.perf_counter() - started) * 1000),
            "page_count": len(pages),
            "raw_result_path": str(raw_path.resolve()),
            "visualization_path": str(visualization.resolve()) if visualization else "",
        }

    def _real_result(self, path: Path, output_dir: Path, visualize: bool, region: tuple[float, float, float, float] | None = None, preprocess_variant: str = "") -> tuple[list[dict[str, Any]], Path | None]:
        from yomitoku.data.functions import load_image, load_pdf
        from yomitoku.utils.misc import save_image

        images = load_pdf(path) if path.suffix.lower() == ".pdf" else load_image(path)
        if len(images) != 1:
            raise UnsupportedInputError("PDF has more than one page")
        original = images[0]
        original_height, original_width = original.shape[:2]
        offset_x, offset_y, scale = 0, 0, 1.0
        image = original
        if region is not None:
            x, y, width, height = region
            offset_x = max(0, min(original_width - 1, int(x * original_width)))
            offset_y = max(0, min(original_height - 1, int(y * original_height)))
            right = max(offset_x + 1, min(original_width, int((x + width) * original_width)))
            bottom = max(offset_y + 1, min(original_height, int((y + height) * original_height)))
            image = original[offset_y:bottom, offset_x:right].copy()
        if preprocess_variant:
            import cv2
            scale = 3.0
            image = cv2.resize(image, None, fx=scale, fy=scale, interpolation=cv2.INTER_CUBIC)
            gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY) if len(image.shape) == 3 else image
            if preprocess_variant == "adaptive_threshold":
                gray = cv2.adaptiveThreshold(gray, 255, cv2.ADAPTIVE_THRESH_GAUSSIAN_C, cv2.THRESH_BINARY, 41, 9)
            else:
                gray = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8)).apply(gray)
            image = cv2.cvtColor(gray, cv2.COLOR_GRAY2BGR)
        result, ocr_visualization, _layout_visualization = self._analyzer(image)
        blocks = []
        for word in result.words:
            xs = [point[0] for point in word.points]
            ys = [point[1] for point in word.points]
            blocks.append({
                "text": word.content,
                "confidence": float(min(word.rec_score, word.det_score)),
                "bbox": [min(xs) / scale + offset_x, min(ys) / scale + offset_y, max(xs) / scale + offset_x, max(ys) / scale + offset_y],
                "role": "text",
                "direction": word.direction,
            })
        vis_path: Path | None = None
        if visualize and ocr_visualization is not None:
            vis_path = output_dir / "ocr-visualization.jpg"
            save_image(ocr_visualization, str(vis_path))
        return [{"width": int(original_width), "height": int(original_height), "blocks": blocks}], vis_path

    def _mock_result(self, path: Path, output_dir: Path, visualize: bool) -> tuple[list[dict[str, Any]], Path | None]:
        sidecar = Path(str(path) + ".mock.json")
        if sidecar.is_file():
            payload = json.loads(sidecar.read_text(encoding="utf-8"))
            pages = payload["pages"]
        else:
            pages = [{"width": 600, "height": 1000, "blocks": [
                {"text": "KIRI LAB書店", "confidence": 0.98, "bbox": [90, 45, 510, 105]},
                {"text": "2026/07/21 10:32", "confidence": 0.97, "bbox": [135, 155, 465, 190]},
                {"text": "登録番号 T1234567890123", "confidence": 0.96, "bbox": [85, 220, 515, 255]},
                {"text": "書籍 サンプル", "confidence": 0.94, "bbox": [80, 410, 320, 445]},
                {"text": "10%対象 ¥1,100", "confidence": 0.95, "bbox": [80, 650, 520, 690]},
                {"text": "消費税10% ¥100", "confidence": 0.95, "bbox": [80, 705, 520, 745]},
                {"text": "合計 ¥1,100", "confidence": 0.99, "bbox": [80, 790, 520, 850]},
                {"text": "VISA **** 1234", "confidence": 0.93, "bbox": [80, 875, 430, 915]},
            ]}]
        vis_path: Path | None = None
        if visualize and path.suffix.lower() != ".pdf":
            vis_path = output_dir / ("ocr-visualization" + path.suffix.lower())
            shutil.copy2(path, vis_path)
        return pages, vis_path

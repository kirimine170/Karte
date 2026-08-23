# Model provenance

These ONNX files are a sherpa-onnx conversion of the PengChengStarling
multilingual streaming ASR model．The original training model and its
Apache-2.0 metadata are pinned at:

https://huggingface.co/stdo/PengChengStarling/tree/d21e331a200a518138599f1ec412b3bb1c919fe9

The converted ONNX artifacts are pinned at:

https://huggingface.co/csukuangfj/sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10/tree/c6726c1147387ad2a11148b33973135d92a55e6c

The historical model card referenced `github.com/yangb05/PengChengStarling`．
That repository now redirects to the maintained upstream at:

https://github.com/PCL-Voice/PengChengStarling

The exact SHA-256 and byte length of every redistributed file are recorded in
`compliance/assets.json`．See `LICENSE` in this directory for the complete
Apache License 2.0 text and the scope of that notice．

The large ONNX files are not stored in this repository．Run
`scripts/fetch-asr-models.sh` from the repository root to download the pinned
`k2-fsa/sherpa-onnx` release asset and verify its SHA-256 checksums before a
distribution build．

#!/usr/bin/env bash
# Download benchmark datasets for Cortex evaluation.
# Usage: ./download.sh [locomo|dmr|longmemeval|all]
#
# Datasets are saved to bench/datasets/ (gitignored).

set -euo pipefail
cd "$(dirname "$0")"

DATASETS_DIR="datasets"
mkdir -p "$DATASETS_DIR"

download_locomo() {
    echo "==> Downloading LOCOMO dataset (CC BY-NC 4.0)..."
    if [ -f "$DATASETS_DIR/locomo10.json" ]; then
        echo "    Already exists, skipping."
        return
    fi
    curl -fsSL -o "$DATASETS_DIR/locomo10.json" \
        "https://github.com/snap-research/locomo/raw/main/data/locomo10.json"
    echo "    Saved to $DATASETS_DIR/locomo10.json"
}

download_dmr() {
    echo "==> Downloading DMR / MSC-Self-Instruct dataset (Apache 2.0)..."
    if [ -f "$DATASETS_DIR/msc_self_instruct.jsonl" ]; then
        echo "    Already exists, skipping."
        return
    fi
    curl -fsSL -o "$DATASETS_DIR/msc_self_instruct.jsonl" \
        "https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct/resolve/main/msc_self_instruct.jsonl"
    echo "    Saved to $DATASETS_DIR/msc_self_instruct.jsonl"
}

download_longmemeval() {
    echo "==> Downloading LongMemEval dataset (MIT)..."
    if [ -d "$DATASETS_DIR/longmemeval" ]; then
        echo "    Already exists, skipping."
        return
    fi
    git clone --depth 1 https://github.com/xiaowu0162/LongMemEval.git "$DATASETS_DIR/longmemeval"
    echo "    Saved to $DATASETS_DIR/longmemeval/"
}

case "${1:-all}" in
    locomo)       download_locomo ;;
    dmr)          download_dmr ;;
    longmemeval)  download_longmemeval ;;
    all)
        download_locomo
        download_dmr
        download_longmemeval
        echo ""
        echo "All datasets downloaded to $DATASETS_DIR/"
        ;;
    *)
        echo "Usage: $0 [locomo|dmr|longmemeval|all]"
        exit 1
        ;;
esac

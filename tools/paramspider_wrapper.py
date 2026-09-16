#!/usr/bin/env python3
"""Run the vendored ParamSpider source without a machine-wide install."""

from __future__ import annotations

import sys
from pathlib import Path


VENDOR_ROOT = Path(__file__).resolve().parent / "vendor" / "ParamSpider-src"
if not VENDOR_ROOT.is_dir():
    raise SystemExit(f"vendored ParamSpider source not found: {VENDOR_ROOT}")

sys.path.insert(0, str(VENDOR_ROOT))
from paramspider.main import main  # noqa: E402


if __name__ == "__main__":
    raise SystemExit(main() or 0)

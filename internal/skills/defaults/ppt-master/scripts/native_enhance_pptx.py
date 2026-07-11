#!/usr/bin/env python3
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "python-pptx>=0.6.21", "XlsxWriter>=3.0.0", "edge-tts>=7.2.8",
#     "PyMuPDF>=1.23.0", "mammoth>=1.6.0", "markdownify>=0.11.6",
#     "ebooklib>=0.18", "nbconvert>=7.0.0", "openpyxl>=3.1.0",
#     "Pillow>=9.0.0", "numpy>=1.20.0", "requests>=2.31.0",
#     "beautifulsoup4>=4.12.0", "curl_cffi>=0.7.0", "flask>=3.0.0",
# ]
# ///
"""
PPT Master - Native Enhance PPTX Entrypoint

Public CLI wrapper for native enhancement of existing PPTX decks. V1 delegates
to the narration/timings implementation while keeping the stable command name
aligned with the native-enhance workflow.

Usage:
    uv run scripts/native_enhance_pptx.py init <source.pptx> [--name project_name]
    uv run scripts/native_enhance_pptx.py plan <project_path>
    uv run scripts/native_enhance_pptx.py validate <project_path>
    uv run scripts/native_enhance_pptx.py apply <project_path>

Examples:
    uv run scripts/native_enhance_pptx.py init projects/source.pptx --name fire_station
    uv run scripts/native_enhance_pptx.py plan projects/fire_station_native_enhance_20260626
    uv run scripts/native_enhance_pptx.py apply projects/fire_station_native_enhance_20260626

Dependencies:
    Same as native_narration_pptx.py.
"""

from __future__ import annotations

import sys
from pathlib import Path

_SCRIPTS_DIR = Path(__file__).resolve().parent
if str(_SCRIPTS_DIR) not in sys.path:
    sys.path.insert(0, str(_SCRIPTS_DIR))

from console_encoding import configure_utf8_stdio  # noqa: E402
from native_narration_pptx import main  # noqa: E402

configure_utf8_stdio()


if __name__ == "__main__":
    raise SystemExit(main())

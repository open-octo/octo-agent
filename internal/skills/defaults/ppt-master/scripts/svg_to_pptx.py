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
"""PPT Master - SVG to PPTX Tool (thin wrapper).

Delegates to the svg_to_pptx package. ``-s final`` remains a native-export
diagnostic override; the standard pipeline reads ``svg_output/``:
    uv run scripts/svg_to_pptx.py <project_path> -s final
"""

import sys
from pathlib import Path

# Ensure the scripts directory is on sys.path so the package can be found
sys.path.insert(0, str(Path(__file__).resolve().parent))

from console_encoding import configure_utf8_stdio
from svg_to_pptx import main

configure_utf8_stdio()

if __name__ == '__main__':
    raise SystemExit(main())

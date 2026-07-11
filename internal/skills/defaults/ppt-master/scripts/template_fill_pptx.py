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
"""PPT Master - PPTX Template Fill (thin wrapper).

Delegates to the template_fill_pptx package. Kept as the CLI entry point so the
documented command paths keep working:

    uv run scripts/template_fill_pptx.py analyze <deck.pptx> -o <stem>.slide_library.json
    uv run scripts/template_fill_pptx.py scaffold <stem>.slide_library.json -o fill_plan.json
    uv run scripts/template_fill_pptx.py check-plan <stem>.slide_library.json fill_plan.json
    uv run scripts/template_fill_pptx.py apply <deck.pptx> fill_plan.json -o output.pptx
    uv run scripts/template_fill_pptx.py validate <project>

Implementation lives in the template_fill_pptx/ package (ooxml, analyzer,
scaffolder, checker, text_fill, table_fill, chart_fill, transitions, notes,
package, applier, validator, cli).
"""

import sys
from pathlib import Path

# Ensure the scripts directory is on sys.path so the package can be found.
sys.path.insert(0, str(Path(__file__).resolve().parent))

from console_encoding import configure_utf8_stdio
from template_fill_pptx import main

configure_utf8_stdio()

if __name__ == "__main__":
    raise SystemExit(main())

# Self-hosted web fonts for the landing page

The landing page hosts its own fonts instead of linking to Google Fonts, so
headings render identically on networks where `fonts.googleapis.com` is
unreachable (notably mainland China). All three families are licensed under
the SIL Open Font License 1.1; the license texts sit next to the files.

| File | Family | Source | Subset |
|---|---|---|---|
| `newsreader-latin.woff2` | Newsreader, variable (`wght` 200–800, `opsz` 6–72) | Google Fonts latin slice of `Newsreader[opsz,wght].ttf` | latin |
| `newsreader-italic-latin.woff2` | Newsreader Italic, variable (`opsz` 6–72) | Google Fonts latin slice | latin |
| `jetbrains-mono-latin.woff2` | JetBrains Mono, variable (`wght` 400–800) | Google Fonts latin slice | latin |
| `noto-serif-sc-medium.subset.woff2` | Noto Serif CJK SC Medium (static, weight 500) | `notofonts/noto-cjk` `Serif/OTF/SimplifiedChinese/NotoSerifCJKsc-Medium.otf` | only the characters used in `index.html` |

The latin files are the exact woff2 slices Google Fonts serves for the
`U+0000-00FF …` unicode-range (the `@font-face` rules in `index.html` repeat
that range), downloaded once and committed unchanged.

## Regenerating the CJK subset

The serif face is only used for headings and the few italic links, so the
Chinese subset is cut to the characters that actually appear on the page.
Whenever Chinese copy in `index.html` changes, rebuild it, otherwise new
characters fall back to the system serif and look mismatched:

```sh
# 1. collect every non-ASCII character on the page (plus printable ASCII and
#    common CJK punctuation) into a text file
python3 - <<'EOF'
src = open('landing/index.html').read()
chars = set(ch for ch in src if ord(ch) > 0x7F)
chars.update(chr(c) for c in range(0x20, 0x7F))
chars.update('，。、：；！？“”‘’（）《》【】—…·～')
open('/tmp/subset-chars.txt', 'w').write(''.join(sorted(chars)))
EOF

# 2. fetch the full Medium OTF (~25 MB, not committed)
curl -sSL -o /tmp/NotoSerifCJKsc-Medium.otf \
  https://github.com/notofonts/noto-cjk/raw/main/Serif/OTF/SimplifiedChinese/NotoSerifCJKsc-Medium.otf

# 3. subset to woff2 (needs fonttools with brotli; uvx pulls it in)
uvx --from "fonttools[woff]" pyftsubset /tmp/NotoSerifCJKsc-Medium.otf \
  --text-file=/tmp/subset-chars.txt --flavor=woff2 --layout-features='*' \
  --no-hinting --desubroutinize \
  --output-file=landing/fonts/noto-serif-sc-medium.subset.woff2
```

The result is ~180 KB for ~440 CJK characters.

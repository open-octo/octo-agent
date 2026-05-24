---
name: skill-add
description: 'Install skills from a zip URL or local zip file path. Use this skill whenever the user wants to install a skill from a zip link or a local file, or uses commands like /skill-add with a URL or file path. Trigger on phrases like: install skill, install from zip, skill from zip, skill from url, add skill from zip, 安装skill, 从zip安装skill, 从本地安装skill.'
disable-model-invocation: false
user-invocable: true
---

# Skill Add — Zip Installer

Installs a skill from a zip URL **or a local zip file path** using the bundled `install_from_zip.rb` script.

## How to Install

The script path is listed in the Supporting Files above (`scripts/install_from_zip.rb`) — use the full path directly, no `find` needed.

The script accepts **either a remote URL or a local file path**:

```bash
ruby "SKILL_DIR/scripts/install_from_zip.rb" <zip_url_or_path> [slug]
```

- `<zip_url_or_path>` — a remote `https://` URL **or** an absolute/relative local path to a `.zip` file
- `[slug]` — optional; the skill's directory name. If omitted, inferred from the filename (e.g. `canvas-design-1.2.0.zip` → `canvas-design`)

The script handles everything automatically:
- For URLs: downloads the zip (follows HTTP redirects)
- For local paths: reads the file directly (no download needed)
- Extracts and locates all `SKILL.md` files inside
- Copies skill directories to `~/.clacky/skills/`
- Reports installed skills with their descriptions

**Do NOT manually download or unzip — the script handles everything.**

## Examples

**From a remote URL:**
```
/skill-add https://store.clacky.ai/skills/canvas-design-1.2.0.zip
```
```bash
ruby "SKILL_DIR/scripts/install_from_zip.rb" \
  "https://store.clacky.ai/skills/canvas-design-1.2.0.zip" \
  "canvas-design"
```

**From a local file:**
```
/skill-add /Users/alice/Downloads/my-skill-1.0.0.zip
```
```bash
ruby "SKILL_DIR/scripts/install_from_zip.rb" \
  "/Users/alice/Downloads/my-skill-1.0.0.zip"
```

## Notes

- Skills install to `~/.clacky/skills/` by default
- Local paths may be absolute (`/path/to/skill.zip`) or use `~` (`~/Downloads/skill.zip`)
- If the user doesn't provide a URL or path, ask them for the zip source

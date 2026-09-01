# Provenance audit — legal-search

## Sources

1. `Golden2002/legal-research-skill`
   (https://github.com/Golden2002/legal-research-skill, commit
   `f2f99327708f095acc4a84bbe69158e85577f93b`, path
   `legal-research/SKILL.md`), MIT — root `LICENSE` file confirmed by direct
   fetch.
2. `lawyerwangbo/legal-assistant-pro`
   (https://github.com/lawyerwangbo/legal-assistant-pro, commit
   `0a9ae7b926bfa080cb65201126e2a4953d50b967`, path `SKILL.md`), MIT — root
   `LICENSE` file confirmed by direct fetch.

## What source 1 actually is

A large (~1,585 line) Chinese-language SKILL.md, `法律检索助手`
("legal-issue-research"), plus a substantial Python codebase under
`legal-research/`: `src/legal_validator.py` (source-hierarchy/timeliness/
citation-format validators), `src/case_retriever.py` (case-keyword
extraction), `src/legal_reasoning.py` (deductive/inductive/analogical/
abductive reasoning helpers), `mcp_server.py` (9 MCP tools wrapping the
above for the author's own "PAEG" multi-agent ecosystem), a `web/` Flask
panel, and `use_database_by_api/` — a full integration with Wolters Kluwer
(威科先行), a paid commercial legal database, including a workflow that
asks the user for API credentials and writes them to `config.json`. The
repo also bundles an unrelated `docx/` skill directory that appears to be a
copy of Anthropic's proprietary `skills/docx` (schemas, redlining
validators, `office/` helpers) — **not used or referenced by this
adaptation in any way**; the license status of that bundled directory was
not investigated because nothing from it was touched.

## What source 2 actually is

A single, extremely dense `SKILL.md` (~190 lines but very information-
dense) covering an entire law-firm practice: litigation intake, case-type
identification (33-category cause-of-action reference), evidence handling,
document drafting (across every litigation stage plus non-litigation
documents), labor law, IP, and non-litigation compliance work — far
broader than the `legal-search` scope. Only its short "3. 法律检索（RAG优先）"
section and the "核心原则" line "引用验证：严禁编造法条案例" were used here, as
independent corroboration of source 1's official-source list and
no-fabrication principle. The rest of source 2's content is used
separately for `legal-compliance` and `legal-drafting` (see those skills'
own `PROVENANCE.md`).

## What was reused

- **Source-of-law hierarchy and search scope** (法律→行政法规→司法解释→部门规章
  →地方性法规→指导性案例→典型案例), translated/reorganized from source 1's
  "2. 全面覆盖法源" and "3.1 检索范围".
- **Six-element statute-citation requirement** (文件名称/文号/生效时间/条款编号/
  原文引用/效力状态) from source 1's "3.2 法条引用要求".
- **Case-citation requirements and the authenticity/no-fabrication rules**
  (indicative case types, source-priority table, prohibited sources) from
  source 1's "3.2.1 案例引用要求" and "12.2 案例真实性验证要求", corroborated by
  source 2's "引用验证：严禁编造法条案例".
- **Cross-reference annotation format** (纵向/横向引用) from source 1's
  "3.3 条文互引梳理".
- **Limitation-period reference table**, cross-checked between source 1's
  "第五阶段：时效与期限梳理" and source 2's `references/limitation-periods.md`
  (both independently list 民事诉讼时效3年/民法典188条, 劳动仲裁1年/劳动争议调解
  仲裁法27条, 再审6个月, 执行2年— agreement between two independently-written
  sources was treated as corroboration, not as license to skip verification
  language in the skill itself).
- **Common-mistakes checklist** from source 1's "十、常见检索错误警示".
- **Free official source list** (国家法律法规数据库/中国裁判文书网/人民法院案例库/
  最高法院官网/全国人大官网/国务院官网), present in both sources; source 2's
  shorter list was used as the base since it excludes paid databases,
  supplemented with source 1's 北大法宝 entry (explicitly noted here as
  partially paid).

## What was removed / deliberately not reused

- **All Python code** (`legal_validator.py`, `case_retriever.py`,
  `legal_reasoning.py`, `mcp_server.py`, the `web/` Flask panel) — this
  skill is prose-only, per octo's "skills compose existing tools, no new
  tool code" convention.
- **The Wolters Kluwer (威科先行) paid-API integration** — an entire workflow
  section ("威科先行 API 确认与配置") that asks the user for API credentials and
  writes `config.json`. Not appropriate for a general-purpose skill
  (assumes a paid subscription most users won't have) and not something
  this skill should be soliciting credentials for.
- **The `docx/` skill bundled in the same repo** — untouched; its own
  license status (likely a copy of Anthropic's proprietary docx skill,
  based on directory contents matching the schema/validator layout seen in
  `internal/skills/defaults/office-xlsx/PROVENANCE.md`'s description of
  that proprietary original) was not relevant to audit since nothing from
  it was used.
- **The forced `AskUserQuestion`-tool identity-intake flow** (普通人/法学生/
  律师/法官检察官/企业法务 branching with mandatory tool calls) — octo's
  `legal-helper` profile has no such tool, and `legal-helper`'s own system
  prompt already adapts to the user's apparent expertise without a rigid
  multi-step intake gate.
- **The docx-export output option** — no `docx` skill exists in octo
  (only `office-xlsx` for spreadsheets); dropped rather than built.
- **Source 1's differentiated report sections per identity** (权利清单/维权
  步骤/检索方法论/类案检索/法条竞合分析 etc., duplicated per role) — this is
  report-formatting logic tightly coupled to the removed identity-intake
  flow; not reused.

## Conclusion

Genuine adaptation of prose methodology and reference tables from two
independent MIT-licensed sources, cross-checked against each other where
they overlap. No code, no paid-database integration, and no
tool-assumption-specific machinery were carried over.

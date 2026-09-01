# Provenance audit — legal-drafting

## Source

`lawyerwangbo/legal-assistant-pro`
(https://github.com/lawyerwangbo/legal-assistant-pro, commit
`0a9ae7b926bfa080cb65201126e2a4953d50b967`), MIT — root `LICENSE` file
confirmed by direct fetch. Same source repository as `legal-compliance`
(see that skill's own `PROVENANCE.md` for what portion of `SKILL.md` was
used there instead).

## Files used

- `SKILL.md` — the "案由匹配流程" summary, the "阶段+当事人地位→文书类型" table
  (仲裁/一审/二审/再审/执行/保全/结案), the "通用排版" convention (标题宋体加粗
  二号居中/正文仿宋小三号/行距28磅/首行缩进2字符), and the per-document rules
  under "诉讼文书起草规范" (起诉状/答辩状/其他文书/商事仲裁申请书) and
  "非诉分支" → "律师函"/"法律意见书"/"合同起草".
- `docs/cause-of-action-reference.md` — the cause-of-action category list
  (十大类，含合同纠纷25项/侵权责任10项/劳动争议7项等子项) and the four-step
  matching methodology (识别法律关系→匹配大类→确定具体案由→交叉验证), plus its
  "常见错误" examples (借贷纠纷 vs 欠款纠纷 misclassification).
- `templates/civil-complaint.md` — the 民事起诉状 skeleton and its "关键规范"
  (诉讼请求写法、事实与理由三段式、排版、注意事项).
- `templates/civil-answer.md` — the 民事答辩状 skeleton and its "关键规范"
  (答辩意见前置、论证策略分层、交通事故专项、"不作为" prohibitions list).
- `templates/appeal.md` — despite its filename, this file's actual content
  in the cloned repository (commit `0a9ae7b926bfa080cb65201126e2a4953d50b967`)
  is titled "其他诉讼文书模板" and bundles six documents: 民事反诉状/民事上诉状/
  再审申请书/强制执行申请书/代理词/财产保全申请书. All six were used.
- `templates/demand-letter.md` — bundles three non-litigation documents:
  律师函/法律意见书/商事仲裁申请书 (including the litigation→arbitration
  terminology substitution table). All three were used.
- A contract-drafting template present in the repository's `templates/`
  directory — despite the filename `defense-statement.md` suggesting
  litigation-defense content, its actual body in the cloned commit is
  "合同起草标准结构" (the 16-item contract skeleton and the five core-clause
  drafting-point sections: 违约责任/争议解决/保密条款/知识产权/通知与送达).
  Used for the "合同起草" section of this skill.

**Note on the source repository's file naming**: two of the four files
read under `templates/` (`appeal.md`, `defense-statement.md`) contain
content that does not match what their filenames suggest — this is an
apparent authoring/naming inconsistency in the upstream repository itself,
observed directly by reading each file's actual body rather than assumed
from its name. All content actually used is attributed above by what it
contains, not by filename.

## What was reused

Document skeletons (field-by-field structure with bracketed placeholders),
the drafting-point commentary attached to each (e.g. LPR 违约金表述顺序,
答辩状的"不作为"清单, 反诉状的当事人标注要求), the stage/party-role document
matrix, the cause-of-action matching methodology, and the general
formatting convention — all translated/reorganized, not copied as inert
boilerplate (the placeholders and Chinese legal-document conventions are
the same regardless of phrasing, since they reflect real court-filing
practice rather than the source author's original expression).

## What was NOT reused

- **Litigation intake and client-communication SOPs** (SPIKES 沟通法、委托
  签约确认、五分钟案情汇报) — not document-drafting content, out of scope.
- **Evidence handling and Claim Chart construction** — covered separately
  by `legal-due-diligence` and `legal-reasoning` (built from different,
  Apache-2.0 sources); not duplicated here.
- **Labor-law and IP practice modules** — out of scope for a general
  drafting skill.
- **仲裁答辩书/上诉答辩状/再审答辩状/执行异议申请书/保全异议申请书** — named in the
  source's stage/party-role table as document types but not given their
  own template body in the source repository (only implied by the table);
  this skill's table still lists them for completeness (matching the
  source), but no drafting template exists for them here since the source
  provides none — noted as a gap rather than filled with invented content.
- **Specific statute article numbers stated as bare facts** (e.g. "依据
  《中华人民共和国民法典》第XXX条" is already a placeholder in the source's
  templates, but a few surrounding commentary lines cite concrete articles)
  — this skill adds an explicit instruction to verify current article
  numbers via the sibling `legal-search` skill before relying on them,
  since this skill's author did not independently re-verify each one
  against current official text.

## Conclusion

A consolidation of one practitioner-authored MIT source's document
templates and drafting rules into a single skill, with filename/content
mismatches in the upstream repository resolved by reading actual file
bodies rather than trusting filenames, and an explicit statute-
verification caveat added where the source's commentary cites specific
article numbers.

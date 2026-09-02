# Provenance audit — legal-compliance

## Source

`lawyerwangbo/legal-assistant-pro`
(https://github.com/lawyerwangbo/legal-assistant-pro, commit
`0a9ae7b926bfa080cb65201126e2a4953d50b967`, path `SKILL.md`), MIT — root
`LICENSE` file confirmed by direct fetch.

## What the source actually is

A single, extremely dense ~190-line `SKILL.md` covering an entire law-firm
practice end to end: litigation intake and case-type classification (33
案由 categories), a full litigation-stage/document-type matrix, evidence
handling and Claim Chart construction, drafting rules for every litigation
and non-litigation document type, a labor-law practice module (dismissal
review, overtime calculation, employment classification, onboarding
review, social-insurance rates), an IP practice module (infringement
screening, invention screening, IP clause review, portfolio management),
and a non-litigation compliance module. This skill uses **only** the
compliance-specific subset of the non-litigation branch.

## What was reused

- **PIPL 14-point self-assessment checklist** — the exact 14 items (合法性
  基础/告知义务/最小必要/敏感信息单独同意/个人信息权利/撤回同意/委托处理/跨境
  传输/安全措施/合规审计/应急预案/保护负责人/未成年人保护), translated into a
  checklist format, from the source's "PIPL合规自查（14项）" line.
- **Product-launch review structure**: the six-step process (获取输入→理解上线
  内容→AI检测→遍历八类框架→校准→组装输出) and the eight-framework checklist
  (合同承诺/PIPL/数据安全/知识产权/第三方合作/行业监管/营销宣传/AI治理), plus the
  "双输出（保密备忘录+净化工单）" output pattern and the four-tier source-
  attribution scheme (已确认/需验证/需精准核实/平台政策), from the source's
  "产品上线审查" line.
- **Marketing-compliance review**: the five-category classification
  (模糊主观/具体事实性/比较性/暗示性/绝对性) and its review flow
  (提取→分类→核实→广告法速查), plus the four-item advertising-law quick
  reference (广告法第9条/第28条, 反不正当竞争法第8条, 消保法第55条), from the
  source's "营销合规审查" line.
- **Product feature risk assessment**: the trigger conditions and six-
  section structure (评估对象→风险点→监管环境→先例→选项→建议), 2-4 page
  scoping guidance, from the source's "产品功能风险评估" line.

Note: the source states each of these as a compressed one-line bullet
(e.g. "PIPL合规自查（14项）：合法性基础/告知义务/..."); this skill expands each
into the fuller checklist/table/workflow form actually usable as
step-by-step instructions, since the source's terse bullet format is a
personal reference note for its author rather than something directly
executable by another user or model without expansion.

## What was NOT reused

- **Litigation intake, drafting, and evidence modules** — reused instead,
  separately, in `legal-drafting` (see that skill's own `PROVENANCE.md`).
- **Labor-law practice module** (dismissal review, overtime calculation,
  employment classification scoring, social-insurance rate tables) — out
  of scope for a general compliance skill; a dedicated labor-law skill
  would be a separate future addition, not built here.
- **IP practice module** (infringement screening, invention screening,
  portfolio management) — out of scope; not reused.
- **Due-diligence workflow** — `legal-due-diligence` in this repo already
  covers batch document review, built independently from a different
  (Apache-2.0, `anthropics/claude-for-legal`) source; not duplicated here.
- **SaaS/subscription contract review section** ("SaaS/订阅协议专项") — this
  is contract-clause review, not compliance assessment; belongs
  conceptually with `contract-review` rather than this skill, and was not
  merged into either to avoid scope creep in this round.
- **Statutory article numbers stated without independent verification** —
  the source states specific 广告法/反不正当竞争法/消保法 article numbers as bare
  facts; this skill keeps them as a starting reference table but adds an
  explicit instruction to verify current article numbers via `legal-search`
  before relying on them in an actual opinion, since statutes are
  periodically renumbered on amendment and this skill's author did not
  independently re-verify each citation against the current official text
  at adaptation time.

## Conclusion

A scoped extraction of the compliance-assessment portion of a much larger
practice-management skill, expanded from compressed reference bullets into
usable step-by-step instructions, with an explicit caveat added about
verifying cited article numbers rather than treating the source's
one-line citations as pre-verified.

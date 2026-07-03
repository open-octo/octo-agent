# @description Triage open issues: categorize, suggest labels, identify missing info, and route to owners. Read-only by default; never closes issues. args: repo (optional path to git repo, defaults to current directory), since (optional "Nd" lookback, default "7d"), limit (optional max issues to triage, default 50).

# ---- inputs -----------------------------------------------------------------
a       = args || {}
repo    = a["repo"].to_s.empty? ? "./" : a["repo"]
since   = a["since"].to_s.empty? ? "7d" : a["since"]
limit   = (a["limit"] || 50).to_i
limit   = 50 if limit <= 0
STATE_REL = ".octo/issue-triage-state.md"

phase "Issue triage: #{repo}"

ISSUE_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "issues" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "number"      => { "type" => "string" },
          "title"       => { "type" => "string" },
          "author"      => { "type" => "string" },
          "body"        => { "type" => "string" },
          "labels"      => { "type" => "array", "items" => { "type" => "string" } },
          "created_at"  => { "type" => "string" },
          "url"         => { "type" => "string" },
        },
        "required" => ["number", "title", "author"],
      },
    },
  },
  "required" => ["issues"],
})

TRIAGE_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "items" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "number"         => { "type" => "string" },
          "category"       => { "type" => "string", "enum" => ["bug", "feature", "question", "duplicate", "docs", "other"] },
          "suggested_labels" => { "type" => "array", "items" => { "type" => "string" } },
          "missing_info"   => { "type" => "array", "items" => { "type" => "string" } },
          "priority"       => { "type" => "string", "enum" => ["low", "medium", "high", "critical"] },
          "owner_guess"    => { "type" => "string" },
          "action"         => { "type" => "string", "enum" => ["label", "comment", "route", "watch", "none"] },
          "comment_draft"  => { "type" => "string" },
        },
        "required" => ["number", "category", "suggested_labels", "priority", "action"],
      },
    },
  },
  "required" => ["items"],
})

# write_report hands the finished markdown to a real agent, since this script
# runs in an IO-free mruby sandbox with no File/Dir access of its own — only
# agent() can touch the filesystem.
def write_report(repo, relative_path, body)
  prompt = [
    "Write a report file at `#{relative_path}` inside the git repository at #{repo}.",
    "Create the parent directory first if it does not already exist. Overwrite any existing file at that path.",
    "Use the content below verbatim, except insert a line \"**Last run:** <today's date>\" right after the top-level heading, using today's actual date.",
    "-----BEGIN CONTENT-----",
    body,
    "-----END CONTENT-----",
  ].join("\n")
  agent(prompt, { "read_only" => false })
end

# ---- discover issues --------------------------------------------------------------
phase "Discover issues"

discover_prompt = [
  "You are triaging open issues for the repository at #{repo}.",
  "List up to #{limit} open issues created or updated in the last #{since}.",
  "Use `gh issue list` if available; otherwise inspect `.github/issues` or local trackers.",
  "For each issue, extract: number, title, author, body (truncated), labels, created_at, url.",
  "Return JSON matching #{ISSUE_SCHEMA}.",
].join("\n")

raw_issues = agent(discover_prompt, { "read_only" => true, "schema" => ISSUE_SCHEMA })
issues = (JSON.parse(raw_issues) rescue { "issues" => [] })["issues"] || []

log "Found #{issues.size} issue(s) to triage."

if issues.empty?
  write_report(repo, STATE_REL, "# Issue Triage State\n\n**Issues triaged:** 0\n\nNothing to triage.\n")
  "issue-triage: no open issues in the last #{since}."
else
  # ---- triage -----------------------------------------------------------------
  phase "Triage"

  triage_prompt = [
    "Triage these #{issues.size} issues:",
    "#{JSON.pretty_generate(issues)}",
    "For each issue, decide:",
    "- category: bug, feature, question, duplicate, docs, other",
    "- suggested_labels: list of labels to add (e.g. [\"bug\", \"needs-repro\"])",
    "- missing_info: what info is missing from the report (empty if none)",
    "- priority: low, medium, high, critical",
    "- owner_guess: best team/person to route to (or empty)",
    "- action: label, comment, route, watch, none",
    "- comment_draft: a polite request for missing info or routing note (empty if action != comment)",
    "Return JSON matching #{TRIAGE_SCHEMA}.",
  ].join("\n")

  raw_triage = agent(triage_prompt, { "read_only" => true, "schema" => TRIAGE_SCHEMA })
  triage_items = (JSON.parse(raw_triage) rescue { "items" => [] })["items"] || []

  # ---- write state --------------------------------------------------------------
  state_lines = ["# Issue Triage State", "", "**Lookback:** #{since}", "**Limit:** #{limit}", ""]
  state_lines << "## Summary by category"
  categories = triage_items.group_by { |i| i["category"] || "other" }
  categories.sort_by { |k, _| k }.each do |cat, cat_items|
    state_lines << "- #{cat}: #{cat_items.size}"
  end
  state_lines << ""

  state_lines << "## Summary by priority"
  priorities = triage_items.group_by { |i| i["priority"] || "low" }
  ["critical", "high", "medium", "low"].each do |p|
    count = priorities[p]&.size || 0
    state_lines << "- #{p}: #{count}"
  end
  state_lines << ""

  state_lines << "## Triaged items"
  triage_items.each do |item|
    state_lines << "### ##{item['number']} — #{item['category']} / #{item['priority']}"
    state_lines << "- **Suggested labels:** #{item['suggested_labels'].join(', ')}"
    state_lines << "- **Missing info:** #{item['missing_info'].empty? ? 'none' : item['missing_info'].join(', ')}"
    state_lines << "- **Owner guess:** #{item['owner_guess'].to_s.empty? ? 'unknown' : item['owner_guess']}"
    state_lines << "- **Action:** #{item['action']}"
    state_lines << "- **Draft comment:** #{item['comment_draft'].to_s.empty? ? 'none' : item['comment_draft']}"
    state_lines << ""
  end

  state_lines << "## Safety note"
  state_lines << "No issue was closed, labeled, or commented on automatically. This is a read-only report."

  write_report(repo, STATE_REL, state_lines.join("\n"))

  # ---- report -------------------------------------------------------------------
  report_lines = ["## Issue Triage Report", ""]
  report_lines << "- **Issues triaged:** #{triage_items.size}"
  report_lines << "- **Lookback:** #{since}"
  report_lines << "- **Critical / High:** #{triage_items.count { |i| %w[critical high].include?(i['priority']) }}"
  report_lines << "- **Need info:** #{triage_items.count { |i| (i['missing_info'] || []).any? }}"
  report_lines << ""
  report_lines << "State written to: `#{STATE_REL}`"
  report_lines << ""
  report_lines << "This is a read-only triage. Apply labels/comments manually after review."

  report_lines.join("\n")
end

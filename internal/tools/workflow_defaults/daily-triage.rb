# @description Run a daily triage loop: discover open issues and recent CI failures, categorize them, draft safe fixes in isolated git worktrees, verify them with a second agent, and write a state report. args: repo (required, path to git repo), since (optional "Nh" or "Nd" lookback, e.g. "24h" or "1d", default "1d").
# @param repo required: Path to the git repo to triage.
# @param since: Lookback window, e.g. "24h" or "1d" (optional — defaults to "1d").

# ---- inputs -----------------------------------------------------------------
a          = args || {}
repo       = a["repo"]
since      = a["since"].to_s.empty? ? "1d" : a["since"]
STATE_REL  = ".octo/daily-triage-state.md"

phase "Daily triage: #{repo} (since #{since})"

# ---- schemas ----------------------------------------------------------------
DISCOVERY_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "items" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "id"          => { "type" => "string" },
          "title"       => { "type" => "string" },
          "source"      => { "type" => "string", "enum" => ["issue", "ci_failure", "commit", "other"] },
          "url"         => { "type" => "string" },
          "summary"     => { "type" => "string" },
          "actionable"  => { "type" => "boolean" },
          "risk_level"  => { "type" => "string", "enum" => ["low", "medium", "high"] },
        },
        "required" => ["id", "title", "source", "summary", "actionable", "risk_level"],
      },
    },
  },
  "required" => ["items"],
})

TRIAGE_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "ready" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "id"           => { "type" => "string" },
          "title"        => { "type" => "string" },
          "reason"       => { "type" => "string" },
          "proposed_fix" => { "type" => "string" },
        },
        "required" => ["id", "title", "reason"],
      },
    },
    "needs_info" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "id"     => { "type" => "string" },
          "title"  => { "type" => "string" },
          "reason" => { "type" => "string" },
        },
        "required" => ["id", "title", "reason"],
      },
    },
    "human" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "id"     => { "type" => "string" },
          "title"  => { "type" => "string" },
          "reason" => { "type" => "string" },
        },
        "required" => ["id", "title", "reason"],
      },
    },
  },
  "required" => ["ready", "needs_info", "human"],
})

FIX_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "applied"     => { "type" => "boolean" },
    "branch"      => { "type" => "string" },
    "summary"     => { "type" => "string" },
    "files"       => { "type" => "array", "items" => { "type" => "string" } },
    "tests_pass"  => { "type" => "boolean" },
  },
  "required" => ["applied", "summary"],
})

VERIFY_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "approved"    => { "type" => "boolean" },
    "findings"    => { "type" => "string" },
    "risk_level"  => { "type" => "string", "enum" => ["low", "medium", "high"] },
  },
  "required" => ["approved", "findings"],
})

# ---- helpers ----------------------------------------------------------------

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

# ---- discovery --------------------------------------------------------------
phase "Discover"

discovery_prompt = [
  "You are running a daily triage loop in the git repository at #{repo}.",
  "Look back #{since} and gather everything that might need attention today.",
  "Use shell commands (gh, git, ls, find, etc.) to inspect:",
  "- Open issues or PRs (try `gh issue list --repo ...` or `gh pr list --repo ...`),",
  "- Recent CI failures (look for failed workflows, recent test logs, or `gh run list`),",
  "- Recent commits that touched risky areas.",
  "If a tool is unavailable, say so and skip that source.",
  "Return a JSON object matching this schema: #{DISCOVERY_SCHEMA}.",
  "Keep items concise: one sentence summary, boolean actionable, and risk_level low/medium/high.",
].join("\n")

raw_discovery = agent(discovery_prompt, { "read_only" => true, "schema" => DISCOVERY_SCHEMA })
discovery = JSON.parse(raw_discovery) rescue { "items" => [] }
items = discovery["items"] || []
log "Discovered #{items.size} item(s)."

if items.empty?
  write_report(repo, STATE_REL, "# Daily Triage State\n\n**Items:** 0\n\nNothing to triage.\n")
  "daily-triage: no items discovered in the last #{since}."
else
  # ---- triage -----------------------------------------------------------------
  phase "Triage"

  triage_prompt = [
    "You are triaging #{items.size} items discovered in a daily loop.",
    "Items:\n#{JSON.pretty_generate(items)}",
    "Categorize each item into exactly one of three buckets:",
    "- ready: safe to draft a fix in an isolated worktree (low risk, clear scope).",
    "- needs_info: unclear or needs more investigation before acting.",
    "- human: high risk, architectural, or requires a human decision.",
    "Return JSON matching #{TRIAGE_SCHEMA}.",
    "For each 'ready' item, include a one-sentence proposed_fix.",
  ].join("\n")

  raw_triage = agent(triage_prompt, { "read_only" => true, "schema" => TRIAGE_SCHEMA })
  triage = JSON.parse(raw_triage) rescue { "ready" => [], "needs_info" => [], "human" => [] }
  ready      = triage["ready"]      || []
  needs_info = triage["needs_info"] || []
  human      = triage["human"]      || []

  log "Ready: #{ready.size}, needs info: #{needs_info.size}, human: #{human.size}."

  # ---- fix + verify ready items -----------------------------------------------
  phase "Draft fixes"

  def fix_and_verify(item, repo, fix_schema, verify_schema)
    item_id  = item["id"]
    title    = item["title"]
    proposed = item["proposed_fix"]

    fix_prompt = [
      "You are in a fresh, isolated git worktree for the repository at #{repo}.",
      "Address this triage item: #{item_id} — #{title}.",
      "Proposed fix: #{proposed}",
      "Make the smallest safe change. Run any relevant tests or build commands.",
      "If the fix is not feasible, set applied:false and explain why.",
      "Return JSON matching #{fix_schema}.",
    ].join("\n")

    raw_fix = agent(fix_prompt, { "isolation" => "worktree", "schema" => fix_schema })
    fix = JSON.parse(raw_fix) rescue { "applied" => false, "summary" => "parse error" }

    return nil unless fix["applied"]

    verify_prompt = [
      "You are an independent verifier reviewing a fix drafted in a git worktree.",
      "Item: #{item_id} — #{title}",
      "Proposed fix: #{proposed}",
      "Worktree branch: #{fix["branch"] || "unknown"}",
      "Files touched: #{Array(fix["files"]).join(", ")}",
      "Fix summary: #{fix["summary"]}",
      "Review the diff for correctness, safety, and test coverage. Do not fix anything yourself.",
      "Return JSON matching #{verify_schema}.",
    ].join("\n")

    raw_verify = agent(verify_prompt, { "read_only" => true, "schema" => verify_schema })
    verify = JSON.parse(raw_verify) rescue { "approved" => false, "findings" => "parse error" }

    status = verify["approved"] ? "✅ approved" : "⚠️ rejected"
    log "#{item_id}: #{status} — #{verify["findings"]}"

    { "item" => item, "fix" => fix, "verify" => verify }
  end

  fixed = parallel(ready) { |item| fix_and_verify(item, repo, FIX_SCHEMA, VERIFY_SCHEMA) }.compact

  # ---- write state --------------------------------------------------------------
  phase "Write state"

  approved = fixed.select { |f| f["verify"]["approved"] }
  rejected = fixed.reject { |f| f["verify"]["approved"] }

  state_lines = ["# Daily Triage State", "", "**Lookback:** #{since}", ""]
  state_lines << "## Summary"
  state_lines << "- Discovered: #{items.size}"
  state_lines << "- Ready: #{ready.size}"
  state_lines << "- Needs info: #{needs_info.size}"
  state_lines << "- Human: #{human.size}"
  state_lines << "- Drafted: #{fixed.size}"
  state_lines << "- Approved: #{approved.size}"
  state_lines << ""

  state_lines << "## Approved fixes (ready for human gate)"
  if approved.empty?
    state_lines << "None."
  else
    approved.each do |f|
      item = f["item"]
      fix  = f["fix"]
      state_lines << "- `#{item["id"]}`: #{item["title"]} — branch `#{fix["branch"]}` — #{fix["summary"]}"
    end
  end
  state_lines << ""

  state_lines << "## Rejected fixes"
  if rejected.empty?
    state_lines << "None."
  else
    rejected.each do |f|
      item   = f["item"]
      verify = f["verify"]
      state_lines << "- `#{item["id"]}`: #{item["title"]} — #{verify["findings"]}"
    end
  end
  state_lines << ""

  state_lines << "## Needs info"
  needs_info.each do |item|
    state_lines << "- `#{item["id"]}`: #{item["title"]} — #{item["reason"]}"
  end
  state_lines << ""

  state_lines << "## Human required"
  human.each do |item|
    state_lines << "- `#{item["id"]}`: #{item["title"]} — #{item["reason"]}"
  end
  state_lines << ""

  state_lines << "## Safety note"
  state_lines << "No branch was merged or issue closed automatically. Review approved branches before merging."

  write_report(repo, STATE_REL, state_lines.join("\n"))

  # ---- report -----------------------------------------------------------------
  phase "Report"

  report_lines = ["## Daily Triage Report", ""]
  report_lines << "- **Discovered:** #{items.size}"
  report_lines << "- **Ready:** #{ready.size}"
  report_lines << "- **Drafted + verified:** #{approved.size}/#{fixed.size}"
  report_lines << "- **Needs info:** #{needs_info.size}"
  report_lines << "- **Human required:** #{human.size}"
  report_lines << ""
  report_lines << "State written to: `#{STATE_REL}`"
  report_lines << ""

  if approved.any?
    report_lines << "### Approved fixes (review before merge)"
    approved.each do |f|
      item = f["item"]
      fix  = f["fix"]
      report_lines << "- `#{item["id"]}`: #{item["title"]} — branch `#{fix["branch"]}`"
    end
    report_lines << ""
  end

  if rejected.any?
    report_lines << "### Rejected fixes"
    rejected.each do |f|
      item   = f["item"]
      verify = f["verify"]
      report_lines << "- `#{item["id"]}`: #{verify["findings"]}"
    end
    report_lines << ""
  end

  report_lines << "### Human required"
  human.each do |item|
    report_lines << "- `#{item["id"]}`: #{item["title"]}"
  end

  report_lines.join("\n")
end

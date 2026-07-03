# @description Watch open PRs: summarize status, flag stale ones, detect merge conflicts, and suggest next actions. Read-only by default; never merges. args: repo (optional path to git repo, defaults to current directory), stale_days (optional days before a PR is considered stale, default 7), limit (optional max PRs to check, default 30).

# ---- inputs -----------------------------------------------------------------
a          = args || {}
repo       = a["repo"].to_s.empty? ? "./" : a["repo"]
stale_days = (a["stale_days"] || 7).to_i
stale_days = 7 if stale_days <= 0
limit      = (a["limit"] || 30).to_i
limit      = 30 if limit <= 0
STATE_REL  = ".octo/pr-babysitter-state.md"

phase "PR Babysitter: #{repo}"

PR_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "prs" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "number"      => { "type" => "string" },
          "title"       => { "type" => "string" },
          "author"      => { "type" => "string" },
          "branch"      => { "type" => "string" },
          "created_at"  => { "type" => "string" },
          "updated_at"  => { "type" => "string" },
          "reviewers"   => { "type" => "array", "items" => { "type" => "string" } },
          "labels"      => { "type" => "array", "items" => { "type" => "string" } },
          "status"      => { "type" => "string", "enum" => ["open", "draft", "changes_requested", "approved"] },
          "mergeable"   => { "type" => "boolean" },
          "url"         => { "type" => "string" },
        },
        "required" => ["number", "title", "author"],
      },
    },
  },
  "required" => ["prs"],
})

PR_ITEM_SCHEMA = {
  "type" => "object",
  "properties" => {
    "number"     => { "type" => "string" },
    "title"      => { "type" => "string" },
    "reason"     => { "type" => "string" },
    "action"     => { "type" => "string" },
  },
  "required" => ["number", "title", "reason"],
}

SUMMARY_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "healthy" => { "type" => "array", "items" => PR_ITEM_SCHEMA },
    "needs_attention" => { "type" => "array", "items" => PR_ITEM_SCHEMA },
    "stale" => { "type" => "array", "items" => PR_ITEM_SCHEMA },
  },
  "required" => ["healthy", "needs_attention", "stale"],
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

# ---- discover open PRs ------------------------------------------------------------
phase "Discover open PRs"

discover_prompt = [
  "You are watching open PRs for the repository at #{repo}.",
  "List up to #{limit} open PRs using `gh pr list` or equivalent.",
  "For each PR, extract: number, title, author, branch, created_at, updated_at, reviewers, labels, status, mergeable, url.",
  "A PR is stale if updated_at is more than #{stale_days} days ago.",
  "Return JSON matching #{PR_SCHEMA}.",
].join("\n")

raw_prs = agent(discover_prompt, { "read_only" => true, "schema" => PR_SCHEMA })
prs = (JSON.parse(raw_prs) rescue { "prs" => [] })["prs"] || []

log "Found #{prs.size} open PR(s)."

if prs.empty?
  write_report(repo, STATE_REL, "# PR Babysitter State\n\n**Open PRs:** 0\n\nNothing to watch.\n")
  "pr-babysitter: no open PRs."
else
  # ---- classify PRs -----------------------------------------------------------
  phase "Classify PRs"

  classify_prompt = [
    "Classify these #{prs.size} open PRs into three buckets:",
    "#{JSON.pretty_generate(prs)}",
    "- healthy: progressing fine, no action needed.",
    "- needs_attention: has merge conflicts, unresolved changes requested, missing reviewer, or CI failing.",
    "- stale: no activity for #{stale_days} days.",
    "For each PR in needs_attention and stale, give a one-line reason and suggested action (e.g. 'ping reviewer', 'rebase', 'ask for update').",
    "Return JSON matching #{SUMMARY_SCHEMA}.",
  ].join("\n")

  raw_summary = agent(classify_prompt, { "read_only" => true, "schema" => SUMMARY_SCHEMA })
  summary = JSON.parse(raw_summary) rescue { "healthy" => [], "needs_attention" => [], "stale" => [] }

  healthy = summary["healthy"] || []
  needs_attention = summary["needs_attention"] || []
  stale = summary["stale"] || []

  # ---- write state --------------------------------------------------------------
  state_lines = ["# PR Babysitter State", "", "**Stale threshold:** #{stale_days} days", ""]
  state_lines << "## Summary"
  state_lines << "- Healthy: #{healthy.size}"
  state_lines << "- Needs attention: #{needs_attention.size}"
  state_lines << "- Stale: #{stale.size}"
  state_lines << ""

  state_lines << "## Needs attention"
  if needs_attention.empty?
    state_lines << "None."
  else
    needs_attention.each do |pr|
      state_lines << "- ##{pr['number']} #{pr['title']}: #{pr['reason']} — action: #{pr['action']}"
    end
  end
  state_lines << ""

  state_lines << "## Stale"
  if stale.empty?
    state_lines << "None."
  else
    stale.each do |pr|
      state_lines << "- ##{pr['number']} #{pr['title']}: #{pr['reason']} — action: #{pr['action']}"
    end
  end
  state_lines << ""

  state_lines << "## Safety note"
  state_lines << "No PR was merged, closed, or rebased automatically. This is a read-only report."

  write_report(repo, STATE_REL, state_lines.join("\n"))

  # ---- report -------------------------------------------------------------------
  report_lines = ["## PR Babysitter Report", ""]
  report_lines << "- **Open PRs:** #{prs.size}"
  report_lines << "- **Healthy:** #{healthy.size}"
  report_lines << "- **Needs attention:** #{needs_attention.size}"
  report_lines << "- **Stale:** #{stale.size}"
  report_lines << ""
  report_lines << "State written to: `#{STATE_REL}`"
  report_lines << ""

  unless needs_attention.empty? && stale.empty?
    report_lines << "### Suggested actions"
    (needs_attention + stale).each do |pr|
      report_lines << "- ##{pr['number']} — #{pr['action']}"
    end
  end

  report_lines.join("\n")
end

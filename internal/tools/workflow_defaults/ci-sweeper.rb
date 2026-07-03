# @description Monitor recent CI failures, classify root causes, optionally retry flaky jobs, and draft fixes for real failures in isolated git worktrees. High-risk: defaults to dry-run and never retries more than once per job. args: repo (optional path to git repo, defaults to current directory), since (optional "Nd" or "Nh" lookback, default "1d"), dry_run (optional boolean; default true), retry_flaky (optional boolean; default false).

# ---- inputs -----------------------------------------------------------------
a           = args || {}
repo        = a["repo"].to_s.empty? ? "./" : a["repo"]
since       = a["since"].to_s.empty? ? "1d" : a["since"]
dry_run     = a["dry_run"] != false
retry_flaky = a["retry_flaky"] == true
STATE_REL   = ".octo/ci-sweeper-state.md"

phase "CI Sweeper: #{repo}"

FAILURE_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "failures" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "id"          => { "type" => "string" },
          "name"        => { "type" => "string" },
          "branch"      => { "type" => "string" },
          "commit"      => { "type" => "string" },
          "failed_at"   => { "type" => "string" },
          "log_url"     => { "type" => "string" },
          "category"    => { "type" => "string", "enum" => ["flaky", "infra", "code", "config", "unknown"] },
          "confidence"  => { "type" => "string", "enum" => ["low", "medium", "high"] },
          "summary"     => { "type" => "string" },
          "safe_to_retry" => { "type" => "boolean" },
        },
        "required" => ["id", "name", "category", "confidence", "summary"],
      },
    },
  },
  "required" => ["failures"],
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

def fix_and_verify_failure(f, repo, fix_schema, verify_schema)
  fix_prompt = [
    "You are in a fresh, isolated git worktree for the repository at #{repo}.",
    "A CI failure points to a code issue. Failure details:",
    "- ID: #{f['id']}",
    "- Name: #{f['name']}",
    "- Branch: #{f['branch']}",
    "- Commit: #{f['commit']}",
    "- Summary: #{f['summary']}",
    "Investigate the failure, reproduce locally if possible, and make the smallest fix.",
    "Run the relevant tests or the failing CI command locally.",
    "Return JSON matching #{fix_schema}.",
  ].join("\n")

  raw_fix = agent(fix_prompt, { "isolation" => "worktree", "schema" => fix_schema })
  fix = JSON.parse(raw_fix) rescue { "applied" => false, "summary" => "parse error" }

  return nil unless fix["applied"]

  verify_prompt = [
    "You are an independent verifier reviewing a CI failure fix drafted in a git worktree.",
    "Failure: #{f['id']} — #{f['name']}",
    "Summary: #{f['summary']}",
    "Worktree branch: #{fix['branch'] || 'unknown'}",
    "Files: #{Array(fix['files']).join(', ')}",
    "Fix summary: #{fix['summary']}",
    "Review the diff. Does it correctly address the failure? Are tests sufficient?",
    "Return JSON matching #{verify_schema}.",
  ].join("\n")

  raw_verify = agent(verify_prompt, { "read_only" => true, "schema" => verify_schema })
  verify = JSON.parse(raw_verify) rescue { "approved" => false, "findings" => "parse error" }

  status = verify["approved"] ? "✅ approved" : "⚠️ rejected"
  log "#{f['id']}: #{status} — #{verify['findings']}"

  { "failure" => f, "fix" => fix, "verify" => verify }
end

# ---- discovery ----------------------------------------------------------------
phase "Discover CI failures"

discover_prompt = [
  "You are monitoring CI failures for the repository at #{repo}.",
  "Look back #{since} and find failed CI runs or test jobs.",
  "Use `gh run list --status failure` or `gh run list --status cancelled` if available; otherwise inspect CI logs or status files.",
  "For each failure, extract: id, name, branch, commit (short sha), failed_at, log_url.",
  "Then classify the failure into one of: flaky, infra, code, config, unknown.",
  "Confidence must be low/medium/high.",
  "A failure is safe_to_retry only if it is flaky, has high confidence, and has not been retried already in this run.",
  "Return JSON matching #{FAILURE_SCHEMA}.",
].join("\n")

raw_failures = agent(discover_prompt, { "read_only" => true, "schema" => FAILURE_SCHEMA })
failures = (JSON.parse(raw_failures) rescue { "failures" => [] })["failures"] || []

flaky   = failures.select { |f| f["category"] == "flaky" && f["safe_to_retry"] }
code    = failures.select { |f| f["category"] == "code" }
infra   = failures.select { |f| f["category"] == "infra" }
config  = failures.select { |f| f["category"] == "config" }
unknown = failures.select { |f| f["category"] == "unknown" }

log "Found #{failures.size} failure(s): flaky=#{flaky.size}, code=#{code.size}, infra=#{infra.size}, config=#{config.size}, unknown=#{unknown.size}."

if failures.empty?
  write_report(repo, STATE_REL, "# CI Sweeper State\n\n**Failures:** 0\n\nCI is green.\n")
  "ci-sweeper: no CI failures in the last #{since}."
else
  # ---- optionally retry flaky failures -----------------------------------------
  phase "Retry flaky failures" unless flaky.empty?

  retried = []
  if retry_flaky && !dry_run && !flaky.empty?
    retry_prompt = [
      "Run `gh run rerun <id>` in the repository at #{repo} for each of these CI run IDs:",
      flaky.map { |f| f["id"] }.join(", "),
      "Report which ones you successfully retried.",
    ].join("\n")
    agent(retry_prompt, { "read_only" => false })
    retried = flaky.map { |f| f["id"] }
    log "Retried #{retried.size} flaky job(s)."
  elsif retry_flaky && dry_run
    log "Would retry #{flaky.size} flaky job(s), but dry_run is true."
  else
    log "Retry disabled. Skipped #{flaky.size} flaky job(s)."
  end

  # ---- draft fixes for code failures in worktree -------------------------------
  phase "Draft fixes for code failures"

  fixes = dry_run ? [] : parallel(code) { |f| fix_and_verify_failure(f, repo, FIX_SCHEMA, VERIFY_SCHEMA) }.compact

  approved_fixes = fixes.select { |x| x["verify"]["approved"] }
  rejected_fixes = fixes.reject { |x| x["verify"]["approved"] }

  # ---- write state --------------------------------------------------------------
  phase "Write state"

  state_lines = ["# CI Sweeper State", "", "**Lookback:** #{since}", "**Mode:** #{dry_run ? 'dry run' : 'apply'}", ""]
  state_lines << "## Failure summary"
  state_lines << "- Total: #{failures.size}"
  state_lines << "- Flaky (safe to retry): #{flaky.size}"
  state_lines << "- Code: #{code.size}"
  state_lines << "- Infra: #{infra.size}"
  state_lines << "- Config: #{config.size}"
  state_lines << "- Unknown: #{unknown.size}"
  state_lines << ""

  state_lines << "## Retried flaky jobs"
  if retry_flaky && !dry_run
    retried.each { |id| state_lines << "- `#{id}`" }
  else
    state_lines << "None applied (dry_run=#{dry_run}, retry_flaky=#{retry_flaky})."
  end
  state_lines << ""

  state_lines << "## Drafted fixes (code failures)"
  if approved_fixes.empty? && rejected_fixes.empty?
    state_lines << (dry_run ? "No fixes drafted because dry_run is true." : "No fixes could be applied.")
  else
    approved_fixes.each do |x|
      f = x["failure"]
      fix = x["fix"]
      state_lines << "- `#{f['id']}`: #{f['name']} — branch `#{fix['branch']}` — #{fix['summary']}"
    end
    rejected_fixes.each do |x|
      f = x["failure"]
      verify = x["verify"]
      state_lines << "- `#{f['id']}`: #{f['name']} — rejected: #{verify['findings']}"
    end
  end
  state_lines << ""

  state_lines << "## Infra / config / unknown failures"
  (infra + config + unknown).each do |f|
    state_lines << "- `#{f['id']}`: #{f['name']} — #{f['category']} (#{f['confidence']}) — #{f['summary']}"
  end
  state_lines << ""

  state_lines << "## Safety note"
  state_lines << "No PR was merged or CI config changed automatically. Retries were limited to flaky jobs. Code fixes require human review before merging."

  write_report(repo, STATE_REL, state_lines.join("\n"))

  # ---- report ---------------------------------------------------------------------
  phase "Report"

  report_lines = ["## CI Sweeper Report", ""]
  report_lines << "- **Mode:** #{dry_run ? 'dry run' : 'apply'}"
  report_lines << "- **Failures:** #{failures.size}"
  report_lines << "- **Flaky (safe to retry):** #{flaky.size}"
  report_lines << "- **Code fixes drafted:** #{fixes.size}"
  report_lines << "- **Approved fixes:** #{approved_fixes.size}"
  report_lines << "- **Retried jobs:** #{retried.size}"
  report_lines << ""
  report_lines << "State written to: `#{STATE_REL}`"
  report_lines << ""

  unless approved_fixes.empty?
    report_lines << "### Approved fixes (review before merge)"
    approved_fixes.each do |x|
      f = x["failure"]
      fix = x["fix"]
      report_lines << "- `#{f['id']}`: branch `#{fix['branch']}`"
    end
    report_lines << ""
  end

  report_lines << "### Infra / config / unknown"
  (infra + config + unknown).each do |f|
    report_lines << "- `#{f['id']}`: #{f['category']} (#{f['confidence']}) — #{f['summary']}"
  end

  report_lines.join("\n")
end

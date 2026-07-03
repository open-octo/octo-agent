# @description Monitor recent CI failures, classify root causes, optionally retry flaky jobs, and draft fixes for real failures in isolated git worktrees. High-risk: defaults to dry-run and never retries more than once per job. args: repo (optional path to git repo, defaults to current directory), since (optional "Nd" or "Nh" lookback, default "1d"), dry_run (optional boolean; default true), retry_flaky (optional boolean; default false).

# ---- inputs -----------------------------------------------------------------
a           = args || {}
repo        = a["repo"].to_s.empty? ? "./" : a["repo"]
since       = a["since"].to_s.empty? ? "1d" : a["since"]
dry_run     = a["dry_run"] != false
retry_flaky = a["retry_flaky"] == true

state_path = File.expand_path(".octo/ci-sweeper-state.md", repo)

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

Dir.mkdir(File.dirname(state_path)) unless Dir.exist?(File.dirname(state_path))

# Discover recent CI failures.
phase "Discover CI failures"

discover_prompt = [
  "You are monitoring CI failures for a repository at #{File.expand_path(repo)}.",
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

flaky = failures.select { |f| f["category"] == "flaky" && f["safe_to_retry"] }
code = failures.select { |f| f["category"] == "code" }
infra = failures.select { |f| f["category"] == "infra" }
config = failures.select { |f| f["category"] == "config" }
unknown = failures.select { |f| f["category"] == "unknown" }

log "Found #{failures.size} failure(s): flaky=#{flaky.size}, code=#{code.size}, infra=#{infra.size}, config=#{config.size}, unknown=#{unknown.size}."

if failures.empty?
  File.write(state_path, "# CI Sweeper State\n\n**Last run:** #{Time.now.utc.iso8601}\n**Failures:** 0\n\nCI is green.\n")
  return "ci-sweeper: no CI failures in the last #{since}."
end

# Optionally retry flaky failures.
phase "Retry flaky failures" unless flaky.empty?

retried = []
if retry_flaky && !dry_run
  flaky.each do |f|
    retry_cmd = "gh run rerun #{f['id']}"
    `"#{retry_cmd}" 2>/dev/null`
    retried << f["id"]
  end
  log "Retried #{retried.size} flaky job(s)."
elsif retry_flaky && dry_run
  log "Would retry #{flaky.size} flaky job(s), but dry_run is true."
else
  log "Retry disabled. Skipped #{flaky.size} flaky job(s)."
end

# Draft fixes for code failures in worktree.
phase "Draft fixes for code failures"

fixes = []
unless dry_run
  code.each do |f|
    fix_prompt = [
      "You are in a fresh, isolated git worktree for the repository at #{File.expand_path(repo)}.",
      "A CI failure points to a code issue. Failure details:",
      "- ID: #{f['id']}",
      "- Name: #{f['name']}",
      "- Branch: #{f['branch']}",
      "- Commit: #{f['commit']}",
      "- Summary: #{f['summary']}",
      "Investigate the failure, reproduce locally if possible, and make the smallest fix.",
      "Run the relevant tests or the failing CI command locally.",
      "Return JSON matching #{FIX_SCHEMA}.",
    ].join("\n")

    raw_fix = agent(fix_prompt, { "isolation" => "worktree", "schema" => FIX_SCHEMA })
    fix = JSON.parse(raw_fix) rescue { "applied" => false, "summary" => "parse error" }

    next unless fix["applied"]

    verify_prompt = [
      "You are an independent verifier reviewing a CI failure fix drafted in a git worktree.",
      "Failure: #{f['id']} — #{f['name']}",
      "Summary: #{f['summary']}",
      "Worktree branch: #{fix['branch'] || 'unknown'}",
      "Files: #{Array(fix['files']).join(', ')}",
      "Fix summary: #{fix['summary']}",
      "Review the diff. Does it correctly address the failure? Are tests sufficient?",
      "Return JSON matching #{VERIFY_SCHEMA}.",
    ].join("\n")

    raw_verify = agent(verify_prompt, { "read_only" => true, "schema" => VERIFY_SCHEMA })
    verify = JSON.parse(raw_verify) rescue { "approved" => false, "findings" => "parse error" }

    fixes << { "failure" => f, "fix" => fix, "verify" => verify }

    status = verify["approved"] ? "✅ approved" : "⚠️ rejected"
    log "#{f['id']}: #{status} — #{verify['findings']}"
  end
end

approved_fixes = fixes.select { |x| x["verify"]["approved"] }
rejected_fixes = fixes.reject { |x| x["verify"]["approved"] }

# Write state.
state_lines = ["# CI Sweeper State", "", "**Last run:** #{Time.now.utc.iso8601}", "**Lookback:** #{since}", "**Mode:** #{dry_run ? 'dry run' : 'apply'}", ""]
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
  state_lines << dry_run ? "No fixes drafted because dry_run is true." : "No fixes could be applied."
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

File.write(state_path, state_lines.join("\n"))

# Report.
report_lines = ["## CI Sweeper Report", ""]
report_lines << "- **Mode:** #{dry_run ? 'dry run' : 'apply'}"
report_lines << "- **Failures:** #{failures.size}"
report_lines << "- **Flaky (safe to retry):** #{flaky.size}"
report_lines << "- **Code fixes drafted:** #{fixes.size}"
report_lines << "- **Approved fixes:** #{approved_fixes.size}"
report_lines << "- **Retried jobs:** #{retried.size}"
report_lines << ""
report_lines << "State written to: `#{state_path}`"
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

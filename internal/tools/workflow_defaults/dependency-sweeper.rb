# @description Check for outdated dependencies, update patch/minor versions in an isolated git worktree, run tests, and leave changes on a branch for human review. args: repo (optional path to git repo, defaults to current directory), ecosystem (optional "go" | "node" | "python" | "auto"; defaults to "auto"), dry_run (optional boolean; when true, only reports; defaults to true).

# ---- inputs -----------------------------------------------------------------
a         = args || {}
repo      = a["repo"].to_s.empty? ? "./" : a["repo"]
ecosystem = a["ecosystem"].to_s.empty? ? "auto" : a["ecosystem"]
dry_run   = a["dry_run"] != false  # default true
STATE_REL = ".octo/dependency-sweeper-state.md"

phase "Dependency sweeper: #{repo}"

ECOSYSTEM_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "ecosystem" => { "type" => "string", "enum" => ["go", "node", "python", "unknown"] },
  },
  "required" => ["ecosystem"],
})

OUTDATED_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "dependencies" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "name"    => { "type" => "string" },
          "current" => { "type" => "string" },
          "latest"  => { "type" => "string" },
          "kind"    => { "type" => "string", "enum" => ["major", "minor", "patch", "unknown"] },
          "safe"    => { "type" => "boolean" },
          "reason"  => { "type" => "string" },
        },
        "required" => ["name", "current", "latest", "kind", "safe"],
      },
    },
  },
  "required" => ["dependencies"],
})

UPDATE_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "applied"     => { "type" => "boolean" },
    "branch"      => { "type" => "string" },
    "summary"     => { "type" => "string" },
    "updated"     => { "type" => "array", "items" => { "type" => "string" } },
    "tests_pass"  => { "type" => "boolean" },
  },
  "required" => ["applied", "summary"],
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

# ---- detect ecosystem -----------------------------------------------------------
phase "Detect ecosystem"

if ecosystem == "auto"
  detect_prompt = [
    "Look at the repository at #{repo} and determine its primary dependency ecosystem.",
    "Check, in priority order: a go.mod file (ecosystem=go), a package.json file (ecosystem=node),",
    "then requirements*.txt or pyproject.toml (ecosystem=python).",
    "If none of these are present, answer unknown.",
    "Return JSON matching #{ECOSYSTEM_SCHEMA}.",
  ].join("\n")
  raw_ecosystem = agent(detect_prompt, { "read_only" => true, "schema" => ECOSYSTEM_SCHEMA })
  ecosystem = (JSON.parse(raw_ecosystem) rescue { "ecosystem" => "unknown" })["ecosystem"] || "unknown"
  log "Detected ecosystem: #{ecosystem}"
end

if ecosystem == "unknown"
  write_report(repo, STATE_REL, "# Dependency Sweeper State\n\n**Ecosystem:** unknown (no go.mod, package.json, or requirements found)\n")
  "dependency-sweeper: could not detect dependency ecosystem."
else
  # ---- discover outdated dependencies --------------------------------------------
  phase "Discover outdated dependencies"

  discover_prompt = [
    "You are checking for outdated dependencies in a #{ecosystem} repository at #{repo}.",
    "Run the appropriate command to list outdated dependencies:",
    "- Go: `go list -u -m all` and filter to direct deps, or `go get -u` dry run",
    "- Node: `npm outdated` or `yarn outdated`",
    "- Python: `pip list --outdated` or `pip-review --local`",
    "For each outdated dependency, report: name, current version, latest version, update kind (major/minor/patch), whether it's safe to auto-update (patch/minor only), and reason.",
    "Return JSON matching #{OUTDATED_SCHEMA}.",
  ].join("\n")

  raw_deps = agent(discover_prompt, { "read_only" => true, "schema" => OUTDATED_SCHEMA })
  deps = (JSON.parse(raw_deps) rescue { "dependencies" => [] })["dependencies"] || []

  safe_deps = deps.select { |d| d["safe"] }
  unsafe_deps = deps.reject { |d| d["safe"] }

  log "Found #{deps.size} outdated dep(s); #{safe_deps.size} safe to update, #{unsafe_deps.size} skipped."

  if deps.empty?
    write_report(repo, STATE_REL, "# Dependency Sweeper State\n\n**Ecosystem:** #{ecosystem}\n**Dependencies:** all up to date.\n")
    "dependency-sweeper: all dependencies up to date."
  else
    # ---- apply updates in worktree if not dry run --------------------------------
    phase dry_run ? "Plan updates (dry run)" : "Apply updates"

    update_result = { "applied" => false, "summary" => "dry run", "updated" => [], "tests_pass" => false, "branch" => "" }

    unless dry_run
      if safe_deps.empty?
        update_result = { "applied" => false, "summary" => "no safe dependencies to update", "updated" => [], "tests_pass" => false, "branch" => "" }
      else
        update_prompt = [
          "You are in a fresh, isolated git worktree for the #{ecosystem} repository at #{repo}.",
          "Update ONLY these safe dependencies (patch/minor, no major versions):",
          "#{JSON.pretty_generate(safe_deps)}",
          "Run the appropriate update command, then run the test/build command to verify.",
          "For Go: `go get -u` for the listed deps, then `go mod tidy` and `go test ./...`.",
          "For Node: `npm update <pkg>` or `yarn upgrade <pkg>`, then `npm test`.",
          "For Python: `pip install -U <pkg>` within the project's constraints, then run tests.",
          "If tests fail, revert the failing dep and keep the rest.",
          "Return JSON matching #{UPDATE_SCHEMA}.",
        ].join("\n")

        raw_update = agent(update_prompt, { "isolation" => "worktree", "schema" => UPDATE_SCHEMA })
        update_result = JSON.parse(raw_update) rescue { "applied" => false, "summary" => "parse error" }
      end
    end

    # ---- write state ------------------------------------------------------------
    state_lines = ["# Dependency Sweeper State", "", "**Ecosystem:** #{ecosystem}", "**Mode:** #{dry_run ? 'dry run' : 'apply'}", ""]
    state_lines << "## Outdated dependencies"
    if deps.empty?
      state_lines << "None."
    else
      deps.each do |d|
        marker = d["safe"] ? "✅" : "⛔"
        state_lines << "- #{marker} `#{d['name']}`: #{d['current']} → #{d['latest']} (#{d['kind']}) — #{d['reason']}"
      end
    end
    state_lines << ""

    state_lines << "## Safe to auto-update"
    if safe_deps.empty?
      state_lines << "None."
    else
      safe_deps.each { |d| state_lines << "- `#{d['name']}` #{d['current']} → #{d['latest']}" }
    end
    state_lines << ""

    state_lines << "## Update result"
    if dry_run
      state_lines << "Dry run: no changes made. Pass `dry_run: false` to apply updates in a worktree."
    else
      state_lines << "- Applied: #{update_result['applied']}"
      state_lines << "- Branch: #{update_result['branch']}"
      state_lines << "- Summary: #{update_result['summary']}"
      state_lines << "- Updated: #{update_result['updated'].join(', ')}"
      state_lines << "- Tests pass: #{update_result['tests_pass']}"
    end
    state_lines << ""

    state_lines << "## Safety note"
    state_lines << "Only patch/minor updates were attempted. Major versions and breaking changes were skipped. No branch was merged automatically."

    write_report(repo, STATE_REL, state_lines.join("\n"))

    # ---- report -------------------------------------------------------------------
    report_lines = ["## Dependency Sweeper Report", ""]
    report_lines << "- **Ecosystem:** #{ecosystem}"
    report_lines << "- **Mode:** #{dry_run ? 'dry run' : 'apply'}"
    report_lines << "- **Outdated:** #{deps.size}"
    report_lines << "- **Safe to auto-update:** #{safe_deps.size}"
    report_lines << "- **Unsafe/major:** #{unsafe_deps.size}"
    report_lines << "- **Applied:** #{update_result['applied']}"
    report_lines << "- **Tests pass:** #{update_result['tests_pass']}"
    report_lines << ""
    report_lines << "State written to: `#{STATE_REL}`"
    report_lines << ""
    report_lines << "Review the branch before merging. Major version updates require manual review."

    report_lines.join("\n")
  end
end

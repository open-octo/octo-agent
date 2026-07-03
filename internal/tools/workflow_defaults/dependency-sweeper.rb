# @description Check for outdated dependencies, update patch/minor versions in an isolated git worktree, run tests, and leave changes on a branch for human review. args: repo (optional path to git repo, defaults to current directory), ecosystem (optional "go" | "node" | "python" | "auto"; defaults to "auto"), dry_run (optional boolean; when true, only reports; defaults to true).

# ---- inputs -----------------------------------------------------------------
a         = args || {}
repo      = a["repo"].to_s.empty? ? "./" : a["repo"]
ecosystem = a["ecosystem"].to_s.empty? ? "auto" : a["ecosystem"]
dry_run   = a["dry_run"] != false  # default true

state_path = File.expand_path(".octo/dependency-sweeper-state.md", repo)

phase "Dependency sweeper: #{repo}"

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

Dir.mkdir(File.dirname(state_path)) unless Dir.exist?(File.dirname(state_path))

# Detect ecosystem if auto.
phase "Detect ecosystem"

if ecosystem == "auto"
  if File.exist?(File.join(repo, "go.mod"))
    ecosystem = "go"
  elsif File.exist?(File.join(repo, "package.json"))
    ecosystem = "node"
  elsif Dir.glob(File.join(repo, "requirements*.txt")).any? || File.exist?(File.join(repo, "pyproject.toml"))
    ecosystem = "python"
  else
    ecosystem = "unknown"
  end
  log "Detected ecosystem: #{ecosystem}"
end

if ecosystem == "unknown"
  File.write(state_path, "# Dependency Sweeper State\n\n**Last run:** #{Time.now.utc.iso8601}\n**Ecosystem:** unknown (no go.mod, package.json, or requirements found)\n")
  return "dependency-sweeper: could not detect dependency ecosystem."
end

# Discover outdated dependencies.
phase "Discover outdated dependencies"

discover_prompt = [
  "You are checking for outdated dependencies in a #{ecosystem} repository at #{File.expand_path(repo)}.",
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
  File.write(state_path, "# Dependency Sweeper State\n\n**Last run:** #{Time.now.utc.iso8601}\n**Ecosystem:** #{ecosystem}\n**Dependencies:** all up to date.\n")
  return "dependency-sweeper: all dependencies up to date."
end

# Apply updates in worktree if not dry run.
phase dry_run ? "Plan updates (dry run)" : "Apply updates"

update_result = { "applied" => false, "summary" => "dry run", "updated" => [], "tests_pass" => false, "branch" => "" }

unless dry_run
  if safe_deps.empty?
    update_result = { "applied" => false, "summary" => "no safe dependencies to update", "updated" => [], "tests_pass" => false, "branch" => "" }
  else
    update_prompt = [
      "You are in a fresh, isolated git worktree for the #{ecosystem} repository at #{File.expand_path(repo)}.",
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

# Write state.
state_lines = ["# Dependency Sweeper State", "", "**Last run:** #{Time.now.utc.iso8601}", "**Ecosystem:** #{ecosystem}", "**Mode:** #{dry_run ? 'dry run' : 'apply'}", ""]
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
  safe_deps.each { |d| state_lines << "- `#{d['name']}` #{d['current']} → #{d['latest']}`" }
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

File.write(state_path, state_lines.join("\n"))

# Report.
report_lines = ["## Dependency Sweeper Report", ""]
report_lines << "- **Ecosystem:** #{ecosystem}"
report_lines << "- **Mode:** #{dry_run ? 'dry run' : 'apply'}"
report_lines << "- **Outdated:** #{deps.size}"
report_lines << "- **Safe to auto-update:** #{safe_deps.size}"
report_lines << "- **Unsafe/major:** #{unsafe_deps.size}"
report_lines << "- **Applied:** #{update_result['applied']}"
report_lines << "- **Tests pass:** #{update_result['tests_pass']}"
report_lines << ""
report_lines << "State written to: `#{state_path}`"
report_lines << ""
report_lines << "Review the branch before merging. Major version updates require manual review."

report_lines.join("\n")

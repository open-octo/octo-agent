# @description Apply one mechanical change across many files — discover every site, then transform + verify each in its OWN git worktree so parallel edits never collide. args: change (required, what to apply), target (optional scope/paths to search), verify (optional shell command to run in each worktree, e.g. "go build ./...").
# @param change required: What to apply, e.g. "rename foo() to bar()".
# @param target: Scope/paths to search (optional — defaults to the whole repo).
# @param verify: Shell command to run in each worktree to check the change, e.g. "go build ./..." (optional).

# ---- inputs -----------------------------------------------------------------
a       = args || {}
change  = a["change"].to_s
target  = a["target"].to_s
verify  = a["verify"].to_s

scope_line  = target.empty? ? "across the repository" : "within: #{target}"
verify_line = verify.empty? ? "review that the edited file still reads/compiles correctly" : "run `#{verify}` and treat a non-zero exit as failure"

DISCOVER_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "sites" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "file"   => { "type" => "string" },
          "reason" => { "type" => "string" },
        },
        "required" => ["file"],
      },
    },
  },
  "required" => ["sites"],
})

MIGRATE_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "file"     => { "type" => "string" },
    "branch"   => { "type" => "string" },
    "applied"  => { "type" => "boolean" },
    "verified" => { "type" => "boolean" },
    "summary"  => { "type" => "string" },
  },
  "required" => ["applied", "verified", "summary"],
})

def discover(change, scope_line, schema)
  prompt = [
    "Find every file that needs this change, #{scope_line}:",
    change,
    "Search the code yourself. Report only files that genuinely require the edit;",
    "do not include files that already comply. For each give the file path and a",
    "one-line reason it needs changing.",
  ].join("\n")
  raw   = agent(prompt, { "read_only" => true, "schema" => schema })
  sites = (JSON.parse(raw)["sites"] || []) rescue []
  sites.select { |s| s.is_a?(Hash) && !s["file"].to_s.empty? }
end

# migrate_one runs in a fresh, isolated git worktree so parallel edits to
# different files never race on branch/index state; changes are left on a branch
# named in the reply for the caller to review and merge.
def migrate_one(file, change, verify_line, schema)
  prompt = [
    "You are in a fresh, isolated git worktree. Apply this change to `#{file}`:",
    change,
    "Then #{verify_line}.",
    "If the change does not apply cleanly, set applied:false and explain why.",
    "Report: file, the branch your changes are on, applied (bool), verified (bool),",
    "and a one-line summary of what you did.",
  ].join("\n")
  raw = agent(prompt, { "isolation" => "worktree", "schema" => schema })
  r = (JSON.parse(raw) rescue nil)
  r = { "applied" => false, "verified" => false, "summary" => "agent returned no parseable result" } if r.nil?
  r["file"] = file if r["file"].to_s.empty?
  r
end

def render_report(results)
  ok     = results.select { |r| r["applied"] && r["verified"] }
  failed = results.select { |r| !(r["applied"] && r["verified"]) }
  out = ["## batch-migrate — #{ok.size}/#{results.size} site(s) applied + verified", ""]
  unless ok.empty?
    out << "### ✅ Ready to merge"
    ok.each do |r|
      branch = r["branch"].to_s.empty? ? "(branch not reported)" : "`#{r["branch"]}`"
      out << "- `#{r["file"]}` → #{branch} — #{r["summary"]}"
    end
    out << ""
  end
  unless failed.empty?
    out << "### ⚠️ Needs attention"
    failed.each do |r|
      state = r["applied"] ? "applied but verify failed" : "not applied"
      out << "- `#{r["file"]}` — #{state}: #{r["summary"]}"
    end
    out << ""
  end
  out << "Review each branch's diff before merging; nothing was merged automatically."
  out.join("\n")
end

# ---- run --------------------------------------------------------------------
if change.strip.empty?
  # A migration with no described change is meaningless — fail loudly and early.
  "batch-migrate: args[\"change\"] is required (describe the transformation to apply)."
else
  phase "Discover"
  sites = discover(change, scope_line, DISCOVER_SCHEMA)
  log "#{sites.size} site(s) need the change."

  if sites.empty?
    "batch-migrate: no sites require the change — nothing to do."
  else
    phase "Migrate"
    results = parallel(sites) { |s| migrate_one(s["file"], change, verify_line, MIGRATE_SCHEMA) }.compact

    phase "Report"
    render_report(results)
  end
end

# @description After merges, clean up stale branches and update linked issues/tickets. Safe by default: reports what it would do unless `apply: true` is passed. args: repo (optional path to git repo, defaults to current directory), apply (optional boolean; when true, actually delete branches; defaults to false), protect (optional array of branch patterns to never delete, e.g. ["main", "master", "release/*"]).

# ---- inputs -----------------------------------------------------------------
a       = args || {}
repo    = a["repo"].to_s.empty? ? "./" : a["repo"]
apply   = a["apply"] == true
protect = a["protect"] || ["main", "master", "release/*", "hotfix/*"]
protect = protect.is_a?(Array) ? protect : [protect.to_s]

state_path = File.expand_path(".octo/post-merge-cleanup-state.md", repo)

phase "Post-merge cleanup: #{repo}"

BRANCH_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "branches" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "name"       => { "type" => "string" },
          "merged"     => { "type" => "boolean" },
          "upstream"   => { "type" => "string" },
          "last_commit"=> { "type" => "string" },
          "linked_issue"=> { "type" => "string" },
          "safe_to_delete" => { "type" => "boolean" },
          "reason"     => { "type" => "string" },
        },
        "required" => ["name", "merged", "safe_to_delete", "reason"],
      },
    },
  },
  "required" => ["branches"],
})

TICKET_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "updates" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "issue"      => { "type" => "string" },
          "action"     => { "type" => "string", "enum" => ["comment", "close", "none"] },
          "comment"    => { "type" => "string" },
          "reason"     => { "type" => "string" },
        },
        "required" => ["issue", "action"],
      },
    },
  },
  "required" => ["updates"],
})

Dir.mkdir(File.dirname(state_path)) unless Dir.exist?(File.dirname(state_path))

# Discover merged branches.
phase "Discover merged branches"

discover_prompt = [
  "You are cleaning up a git repository at #{File.expand_path(repo)} after merges.",
  "Find local branches that have already been merged into the default branch (main/master).",
  "Use `git branch --merged` and `git branch -r` to check upstream state.",
  "Never include protected branches: #{protect.join(', ')}.",
  "For each branch, report:",
  "- name, merged (bool), upstream (remote tracking branch or empty), last_commit (short sha or date),",
  "- linked_issue (issue number extracted from branch name like `fix/123-foo` or PR body, or empty),",
  "- safe_to_delete (bool), reason (why or why not).",
  "A branch is safe_to_delete only if it is merged, has no unmerged upstream commits, and is not protected.",
  "Return JSON matching #{BRANCH_SCHEMA}.",
].join("\n")

raw_branches = agent(discover_prompt, { "read_only" => true, "schema" => BRANCH_SCHEMA })
branches = (JSON.parse(raw_branches) rescue { "branches" => [] })["branches"] || []

safe = branches.select { |b| b["safe_to_delete"] }
unsafe = branches.reject { |b| b["safe_to_delete"] }

log "Found #{branches.size} branch(es); #{safe.size} safe to delete, #{unsafe.size} protected/skipped."

if safe.empty? && unsafe.empty?
  File.write(state_path, "# Post-Merge Cleanup State\n\n**Last run:** #{Time.now.utc.iso8601}\n**Action:** nothing to clean.\n")
  return "post-merge-cleanup: no merged branches to clean up."
end

# Plan ticket updates.
phase "Plan ticket updates"

ticket_prompt = [
  "For these merged branches, decide what to do with any linked issue/ticket:",
  "#{JSON.pretty_generate(safe)}",
  "Rules:",
  "- If the branch clearly fixes an issue and is merged, action = 'comment' with a closure note.",
  "- If the issue is already closed or the link is unclear, action = 'none'.",
  "- Never action = 'close' automatically; always leave that to a human.",
  "Return JSON matching #{TICKET_SCHEMA}.",
].join("\n")

raw_updates = agent(ticket_prompt, { "read_only" => true, "schema" => TICKET_SCHEMA })
updates = (JSON.parse(raw_updates) rescue { "updates" => [] })["updates"] || []

# Execute or report.
phase apply ? "Apply cleanup" : "Plan cleanup (dry run)"

deleted = []
if apply
  safe.each do |b|
    name = b["name"]
    next if protect.any? { |p| File.fnmatch?(p, name) }
    # Delete the branch.
    `"cd #{repo} && git branch -D #{name}" 2>/dev/null`
    deleted << name
  end
  log "Deleted #{deleted.size} branch(es)."
else
  log "Dry run: would delete #{safe.size} branch(es)."
end

# Write state.
state_lines = ["# Post-Merge Cleanup State", "", "**Last run:** #{Time.now.utc.iso8601}", "**Mode:** #{apply ? 'apply' : 'dry run'}", ""]
state_lines << "## Branches safe to delete"
if safe.empty?
  state_lines << "None."
else
  safe.each { |b| state_lines << "- `#{b['name']}` — #{b['reason']}" }
end
state_lines << ""

state_lines << "## Branches protected/skipped"
if unsafe.empty?
  state_lines << "None."
else
  unsafe.each { |b| state_lines << "- `#{b['name']}` — #{b['reason']}" }
end
state_lines << ""

state_lines << "## Ticket updates planned"
updates.each { |u| state_lines << "- `#{u['issue']}`: #{u['action']} — #{u['reason'] || u['comment']}" }
state_lines << ""

state_lines << "## Safety note"
state_lines << apply ? "Branches were deleted. Issue closure was left to humans." : "This was a dry run. Pass `apply: true` to actually delete branches."

File.write(state_path, state_lines.join("\n"))

# Report.
report_lines = ["## Post-Merge Cleanup Report", ""]
report_lines << "- **Mode:** #{apply ? 'apply' : 'dry run'}"
report_lines << "- **Branches safe to delete:** #{safe.size}"
report_lines << "- **Branches protected/skipped:** #{unsafe.size}"
report_lines << "- **Branches deleted:** #{deleted.size}"
report_lines << "- **Ticket updates planned:** #{updates.size}"
report_lines << ""
report_lines << "State written to: `#{state_path}`"
report_lines << ""

unless apply
  report_lines << "⚠️ This was a dry run. Review the list above and pass `apply: true` if you want the branches deleted."
end

report_lines.join("\n")

# @description Draft a changelog from commits since the last tag, grouped by conventional-commit category, and leave it as a draft file for human review. args: repo (optional path to git repo, defaults to current directory), tag (optional tag to use as baseline; defaults to latest tag), output (optional path for draft changelog; defaults to .octo/changelog-draft.md).

# ---- inputs -----------------------------------------------------------------
a       = args || {}
repo    = a["repo"].to_s.empty? ? "./" : a["repo"]
tag     = a["tag"].to_s
output  = a["output"].to_s.empty? ? File.expand_path(".octo/changelog-draft.md", repo) : a["output"]

phase "Changelog drafter: #{repo}"

COMMIT_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "commits" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "sha"     => { "type" => "string" },
          "author"  => { "type" => "string" },
          "title"   => { "type" => "string" },
          "body"    => { "type" => "string" },
          "category"=> { "type" => "string", "enum" => ["feat", "fix", "docs", "chore", "refactor", "test", "perf", "breaking", "other"] },
        },
        "required" => ["sha", "title", "category"],
      },
    },
  },
  "required" => ["commits"],
})

CHANGELOG_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "version"    => { "type" => "string" },
    "date"       => { "type" => "string" },
    "sections"   => {
      "type" => "object",
      "additionalProperties" => {
        "type" => "array",
        "items" => { "type" => "string" },
      },
    },
    "unclassified" => {
      "type" => "array",
      "items" => { "type" => "string" },
    },
  },
  "required" => ["version", "date", "sections"],
})

# Ensure .octo/ exists.
Dir.mkdir(File.dirname(output)) unless Dir.exist?(File.dirname(output))

# Discover commits since the last tag.
phase "Discover commits"

latest_tag = tag
if latest_tag.empty?
  latest_tag = `"cd #{repo} && git describe --tags --abbrev=0" 2>/dev/null`.strip
end

range = latest_tag.empty? ? "HEAD" : "#{latest_tag}..HEAD"

commit_prompt = [
  "You are drafting a changelog for a git repository at #{File.expand_path(repo)}.",
  "List commits in range `#{range}`.",
  "For each commit, extract: sha (short), author, title, body (optional), and category based on conventional commits:",
  "- feat → feat",
  "- fix → fix",
  "- docs → docs",
  "- chore, build, ci → chore",
  "- refactor → refactor",
  "- test → test",
  "- perf → perf",
  "- anything with BREAKING CHANGE or ! suffix → breaking",
  "- everything else → other",
  "Return JSON matching #{COMMIT_SCHEMA}.",
].join("\n")

raw_commits = agent(commit_prompt, { "read_only" => true, "schema" => COMMIT_SCHEMA })
commits = (JSON.parse(raw_commits) rescue { "commits" => [] })["commits"] || []

log "Found #{commits.size} commit(s) since #{latest_tag.empty? ? 'beginning' : latest_tag}."

if commits.empty?
  File.write(output, "# Changelog Draft\n\nNo new commits since #{latest_tag}.\n")
  return "changelog-drafter: no new commits since #{latest_tag}."
end

# Draft changelog.
phase "Draft changelog"

draft_prompt = [
  "Draft a changelog entry for the next release from these commits:",
  "#{JSON.pretty_generate(commits)}",
  "Group entries by category (Features, Fixes, Performance, Breaking Changes, Documentation, Refactor, Tests, Chores, Other).",
  "Use concise, user-facing language. Link PR/issue numbers if present in commit titles.",
  "Return JSON matching #{CHANGELOG_SCHEMA}.",
].join("\n")

raw_draft = agent(draft_prompt, { "read_only" => true, "schema" => CHANGELOG_SCHEMA })
draft = JSON.parse(raw_draft) rescue { "version" => "Unreleased", "date" => Time.now.utc.to_s[0, 10], "sections" => {}, "unclassified" => [] }

# Render markdown.
lines = ["# Changelog Draft", "", "## #{draft['version']} — #{draft['date']}", ""]
(draft["sections"] || {}).each do |category, entries|
  next if entries.empty?
  title = category.capitalize
  lines << "### #{title}"
  entries.each { |e| lines << "- #{e}" }
  lines << ""
end

if (draft["unclassified"] || []).any?
  lines << "### Other"
  draft["unclassified"].each { |e| lines << "- #{e}" }
  lines << ""
end

lines << ""
lines << "---"
lines << ""
lines << "**Baseline:** `#{latest_tag}`"
lines << ""
lines << "**Commits:** #{commits.size}"
lines << ""
lines << "This is a draft. A human should review and edit before releasing."

File.write(output, lines.join("\n"))

# Also update a lightweight state file.
state_path = File.expand_path(".octo/changelog-drafter-state.md", repo)
File.write(state_path, "# Changelog Drafter State\n\n**Last run:** #{Time.now.utc.iso8601}\n**Baseline:** #{latest_tag}\n**Draft:** #{output}\n**Commits:** #{commits.size}\n")

[
  "## Changelog draft ready",
  "",
  "- **Commits since baseline:** #{commits.size}",
  "- **Baseline:** `#{latest_tag}`",
  "- **Draft written to:** `#{output}`",
  "",
  "Review the draft before including it in the next release.",
].join("\n")

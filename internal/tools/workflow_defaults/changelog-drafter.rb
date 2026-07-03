# @description Draft a changelog from commits since the last tag, grouped by conventional-commit category, and leave it as a draft file for human review. args: repo (optional path to git repo, defaults to current directory), tag (optional tag to use as baseline; defaults to latest tag), output (optional path for draft changelog; defaults to .octo/changelog-draft.md).

# ---- inputs -----------------------------------------------------------------
a       = args || {}
repo    = a["repo"].to_s.empty? ? "./" : a["repo"]
tag     = a["tag"].to_s
output  = a["output"].to_s.empty? ? ".octo/changelog-draft.md" : a["output"]
STATE_REL = ".octo/changelog-drafter-state.md"

phase "Changelog drafter: #{repo}"

COMMIT_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "baseline_tag" => { "type" => "string" },
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

# write_file_content and write_report hand finished text to a real agent,
# since this script runs in an IO-free mruby sandbox with no File/Dir access
# of its own — only agent() can touch the filesystem.
def write_file_content(repo, relative_path, body)
  prompt = [
    "Write the following content verbatim to the file `#{relative_path}` inside the git repository at #{repo}.",
    "Create the parent directory first if it does not already exist. Overwrite any existing file at that path.",
    "-----BEGIN CONTENT-----",
    body,
    "-----END CONTENT-----",
  ].join("\n")
  agent(prompt, { "read_only" => false })
end

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

# ---- discover commits since the last tag --------------------------------------
phase "Discover commits"

baseline_line = tag.empty? ?
  "Determine the latest git tag with `git describe --tags --abbrev=0`; if the repository has no tags, use the beginning of history. Report the tag you used as \"baseline_tag\" (empty string if none)." :
  "Use `#{tag}` as the baseline tag and report it back as \"baseline_tag\"."

commit_prompt = [
  "You are drafting a changelog for the git repository at #{repo}.",
  baseline_line,
  "List every commit between that baseline and HEAD.",
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
parsed_commits = JSON.parse(raw_commits) rescue {}
commits = parsed_commits["commits"] || []
latest_tag = parsed_commits["baseline_tag"].to_s

log "Found #{commits.size} commit(s) since #{latest_tag.empty? ? 'beginning' : latest_tag}."

if commits.empty?
  write_file_content(repo, output, "# Changelog Draft\n\nNo new commits since #{latest_tag.empty? ? 'beginning' : latest_tag}.\n")
  "changelog-drafter: no new commits since #{latest_tag.empty? ? 'beginning' : latest_tag}."
else
  # ---- draft changelog ------------------------------------------------------------
  phase "Draft changelog"

  draft_prompt = [
    "Draft a changelog entry for the next release from these commits:",
    "#{JSON.pretty_generate(commits)}",
    "Group entries by category (Features, Fixes, Performance, Breaking Changes, Documentation, Refactor, Tests, Chores, Other).",
    "Use concise, user-facing language. Link PR/issue numbers if present in commit titles.",
    "Return JSON matching #{CHANGELOG_SCHEMA}.",
  ].join("\n")

  raw_draft = agent(draft_prompt, { "read_only" => true, "schema" => CHANGELOG_SCHEMA })
  draft = JSON.parse(raw_draft) rescue { "version" => "Unreleased", "date" => "TBD", "sections" => {}, "unclassified" => [] }

  # ---- render markdown --------------------------------------------------------------
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

  write_file_content(repo, output, lines.join("\n"))

  write_report(repo, STATE_REL, "# Changelog Drafter State\n\n**Baseline:** #{latest_tag}\n**Draft:** #{output}\n**Commits:** #{commits.size}\n")

  [
    "## Changelog draft ready",
    "",
    "- **Commits since baseline:** #{commits.size}",
    "- **Baseline:** `#{latest_tag}`",
    "- **Draft written to:** `#{output}`",
    "",
    "Review the draft before including it in the next release.",
  ].join("\n")
end

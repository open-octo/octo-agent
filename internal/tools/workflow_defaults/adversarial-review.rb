# @description Multi-dimensional code review — reads the latest git diff and reviews it across correctness, security, performance, readability, and testing, then produces a structured report. args (all optional): pr_branch (compare origin/main...branch), revision (custom git diff range), output (markdown file path), max_lines (default 800), dimensions (hash of dimension -> prompt).

repo = (args || {})["repo"] || "the current directory"
pr_branch = (args || {})["pr_branch"]
revision = (args || {})["revision"]

if pr_branch && !pr_branch.empty?
  revision = "origin/main...#{pr_branch}"
  log("PR branch mode: #{pr_branch}")
elsif revision && !revision.empty?
  log("revision: #{revision}")
else
  revision = "HEAD~1 HEAD"
  log("default revision: #{revision}")
end

max_lines = (args || {})["max_lines"] || 800
output = (args || {})["output"]

dimensions = (args || {})["dimensions"] || {
  "correctness" => "correctness: logic errors, wrong conditionals, off-by-one, unhandled nil/error paths, broken edge cases, concurrency issues",
  "security" => "security: injection, missing authorization, leaked secrets, unsafe deserialization, unvalidated input, path traversal, SSRF",
  "performance" => "performance: N+1 queries, needless allocation in hot paths, blocking calls, accidental O(n^2), repeated IO",
  "readability" => "readability: naming, function length, comments, code organization, Go style, duplicated code, complexity",
  "testing" => "testing: missing coverage for the change, assertions that can't fail, wrong expected values, missing boundary tests"
}

phase("Read diff")
log("repo: #{repo}, revision: #{revision}")

diff = agent("In #{repo}, run `git fetch origin` (continue if it fails), then run `git diff #{revision} --stat` and `git diff #{revision}`. Return the full diff text. If it exceeds #{max_lines} lines, truncate to #{max_lines} lines and note that truncation.")
log("diff length: #{diff.length} chars")

if diff.strip.empty?
  log("diff is empty")
  "No diff to review."
else
  phase("Multi-dimensional review")
  reviews = parallel(dimensions.to_a) do |dim|
    name = dim[0]
    prompt = dim[1]
    log("start #{name} review")
    result = agent("You are a senior code reviewer. Review the following diff only for #{prompt}.\n\nRules:\n- List only concrete problems you found (include line number / function name and a short explanation)\n- If no problems, say 'No issues'\n- Do not speak in generalities\n\n```diff\n" + diff + "\n```")
    log("finish #{name} review")
    "## " + name + "\n" + result
  end

  phase("Summarize report")
  report = agent("Summarize the following multi-dimensional code review into a structured report with:\n1. Overall risk level (low/medium/high)\n2. Issues found per dimension, sorted by severity\n3. Top 3 priority fixes\n\n" + reviews.join("\n\n---\n\n"))

  phase("Output report")
  if output && !output.empty?
    log("write report to: #{output}")
    agent("Use write_file to write the following review report to #{output} as a markdown file.\n\n#{report}")
    log("report written")
    "Review complete. Report written to #{output}\n\n" + report
  else
    log("no output path, return report directly")
    report
  end
end

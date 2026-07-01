# @description Adversarial code review — fan out findings across dimensions, then kill false positives with independent skeptic votes. args (all optional): target (what to review, default = uncommitted diff), dimensions (array), votes (int, default 3).

# ---- inputs -----------------------------------------------------------------
a      = args || {}
target = a["target"] || "the uncommitted changes (run `git diff HEAD`) in this repository"
dims   = a["dimensions"] || ["correctness", "security", "performance", "tests"]
votes  = (a["votes"] || 3).to_i
votes  = 3 if votes < 1

# Per-dimension focus. A dimension not listed here (passed via args) falls back
# to reviewing for its own name.
DIM_FOCUS = {
  "correctness" => "logic errors, wrong conditionals, off-by-one, unhandled nil/error paths, broken edge cases",
  "security"    => "injection, missing authorization, leaked secrets, unsafe deserialization, unvalidated input",
  "performance" => "N+1 queries, needless allocation in hot paths, blocking calls, accidental O(n^2)",
  "tests"       => "missing coverage for the change, assertions that can't fail, wrong expected values",
}

FINDINGS_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "findings" => {
      "type" => "array",
      "items" => {
        "type" => "object",
        "properties" => {
          "file"     => { "type" => "string" },
          "line"     => { "type" => "integer" },
          "severity" => { "type" => "string", "enum" => ["high", "medium", "low"] },
          "title"    => { "type" => "string" },
          "detail"   => { "type" => "string" },
        },
        "required" => ["file", "title", "detail"],
      },
    },
  },
  "required" => ["findings"],
})

VERDICT_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "refuted" => { "type" => "boolean" },
    "reason"  => { "type" => "string" },
  },
  "required" => ["refuted", "reason"],
})

# ---- helpers ----------------------------------------------------------------

# find_stage runs one dimension's reviewer and returns its findings array.
def find_stage(dim, focus, target)
  prompt = [
    "You are reviewing #{target}.",
    "Inspect the diff yourself and read the surrounding code for context.",
    "Report ONLY real defects in the *#{dim}* dimension: #{focus}.",
    "Be conservative — do not invent issues to fill a quota; an empty list is a valid answer.",
    "For each defect give: file, line (best estimate), severity, a one-line title, and a concrete failure scenario in detail.",
  ].join("\n")
  raw = agent(prompt, { "read_only" => true, "schema" => FINDINGS_SCHEMA })
  (JSON.parse(raw)["findings"] || []) rescue []
end

# dedup collapses findings that point at the same file + title.
def dedup(all)
  seen = {}
  out  = []
  all.each do |f|
    next unless f.is_a?(Hash)
    key = "#{f["file"].to_s.downcase}::#{f["title"].to_s.downcase.strip}"
    next if seen[key]
    seen[key] = true
    out << f
  end
  out
end

# survives? runs `votes` independent skeptics, each told to REFUTE the finding,
# and keeps it only if a strict majority fail to refute it.
def survives?(f, votes)
  loc = f["line"] ? "#{f["file"]}:#{f["line"]}" : f["file"].to_s
  claim = "#{loc} — #{f["title"]}: #{f["detail"]}"
  verdicts = parallel((1..votes).to_a) do |_i|
    prompt = [
      "A reviewer claims this is a real defect:",
      claim,
      "Your job is to REFUTE it. Read the actual code to check.",
      "It is refuted if it is wrong, already handled elsewhere, unreachable, or not actually a defect.",
      "Default to refuted:true when uncertain — the bar to keep a finding is that it clearly survives scrutiny.",
      "Return refuted (bool) and a one-line reason.",
    ].join("\n")
    raw = agent(prompt, { "read_only" => true, "schema" => VERDICT_SCHEMA })
    (JSON.parse(raw)["refuted"] ? 1 : 0) rescue 1   # treat a broken verdict as "refuted"
  end
  kept = verdicts.select { |v| v == 0 }.size
  kept > (votes / 2)
end

def sev_rank(s)
  ({ "high" => 0, "medium" => 1, "low" => 2 }[s.to_s.downcase]) || 3
end

# ---- run --------------------------------------------------------------------

phase "Review"
per_dim = parallel(dims) { |d| find_stage(d, DIM_FOCUS[d] || d, target) }
unique  = dedup(per_dim.flatten.compact)
log "#{unique.size} candidate finding(s) after dedup across #{dims.size} dimension(s)"

phase "Verify"
survivors = []
if unique.empty?
  log "No candidates to verify."
else
  judged    = parallel(unique) { |f| { "f" => f, "keep" => survives?(f, votes) } }
  survivors = judged.select { |r| r["keep"] }.map { |r| r["f"] }
  log "#{survivors.size} of #{unique.size} finding(s) survived #{votes}-vote scrutiny."
end

phase "Report"
if survivors.empty?
  "No defects survived adversarial review (#{unique.size} candidate(s) raised, all refuted)."
else
  out = ["## Adversarial review — #{survivors.size} confirmed finding(s)", ""]
  survivors.sort_by { |f| sev_rank(f["severity"]) }.each do |f|
    loc = f["line"] ? "#{f["file"]}:#{f["line"]}" : f["file"].to_s
    out << "### [#{(f["severity"] || "?").to_s.upcase}] #{f["title"]}"
    out << "`#{loc}`"
    out << ""
    out << f["detail"].to_s
    out << ""
  end
  out.join("\n")
end

# @description Map a codebase in parallel — fan out one reader per subsystem, then synthesize a single architecture map. args: target (required, what to map), subsystems (array to skip auto-detection), focus (a question to bias the map toward).
# @param target required: Path or name of the repository / directory to map.
# @param subsystems: Array of subsystem names, to skip auto-detection (optional).
# @param focus: A question to bias the map toward (optional).

# ---- inputs -----------------------------------------------------------------
a      = args || {}
target = a["target"]
focus  = a["focus"].to_s

SUBSYS_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "subsystems" => { "type" => "array", "items" => { "type" => "string" } },
  },
  "required" => ["subsystems"],
})

READER_SCHEMA = JSON.generate({
  "type" => "object",
  "properties" => {
    "name"         => { "type" => "string" },
    "purpose"      => { "type" => "string" },
    "key_files"    => { "type" => "array", "items" => { "type" => "string" } },
    "entry_points" => { "type" => "array", "items" => { "type" => "string" } },
    "depends_on"   => { "type" => "array", "items" => { "type" => "string" } },
    "notes"        => { "type" => "string" },
  },
  "required" => ["name", "purpose"],
})

# ---- 1. decide what the subsystems are --------------------------------------
phase "Survey"
subsystems = a["subsystems"]
if !(subsystems.is_a?(Array)) || subsystems.empty?
  prompt = [
    "Identify the main subsystems / top-level modules of #{target} worth mapping separately.",
    "Look at the directory layout and build files; group by responsibility, not by every folder.",
    "Return up to ~12 of the most important, as an array of short names or paths.",
  ].join("\n")
  raw = agent(prompt, { "read_only" => true, "schema" => SUBSYS_SCHEMA })
  subsystems = (JSON.parse(raw)["subsystems"] || []) rescue []
end
subsystems = subsystems.uniq
log "Mapping #{subsystems.size} subsystem(s) of #{target}."

# ---- 2. read each subsystem in parallel -------------------------------------
phase "Read"
focus_line = focus.empty? ? "" : "Pay special attention to: #{focus}."
summaries = parallel(subsystems) do |s|
  prompt = [
    "Map the *#{s}* subsystem of #{target}. Read its code — do not guess.",
    "Return: its purpose (1-2 sentences), key files (paths), entry points,",
    "what it depends on (other subsystems / notable external deps), and any",
    "notable patterns or gotchas a newcomer should know.",
    focus_line,
  ].join("\n")
  raw = agent(prompt, { "read_only" => true, "schema" => READER_SCHEMA })
  JSON.parse(raw) rescue nil
end
summaries = summaries.compact

# ---- 3. synthesize one architecture map -------------------------------------
phase "Synthesize"
if summaries.empty?
  "Could not map #{target}: no subsystems were identified or read."
else
  focus_hint = focus.empty? ? "" : "Frame the map around this question: #{focus}.\n"
  prompt = [
    "You are writing the architecture map for #{target}.",
    focus_hint + "Below are structured summaries of each subsystem (JSON).",
    "Synthesize ONE coherent markdown document, do not just concatenate them:",
    "1. A one-paragraph overview of what the system is and how it's shaped.",
    "2. A subsystem table: name | purpose | key entry point(s).",
    "3. How the pieces connect — the main dependency / data-flow story.",
    "4. \"Where to start reading\" — an ordered path for a newcomer.",
    "Cite real file paths from the summaries. Do not invent components.",
    "",
    "Subsystem summaries:",
    JSON.generate(summaries),
  ].join("\n")
  agent(prompt, { "read_only" => true })
end

# Workflow DSL prelude — prepended to every user script by internal/workflow.
# Defines the primitives (agent / parallel / pipeline / log / budget_remaining)
# on top of the host imports (__agent_*) the wasm runtime binds. Lives in Go
# (go:embed) so the DSL can evolve without rebuilding mruby.wasm.

# $__wf_sched is true only while the cooperative event loop is driving fibers.
# It lets agent() pick the right path: yield cooperatively inside parallel/
# pipeline, but run synchronously when called at top level (no fiber to yield).
$__wf_sched = false

# $__wf_ready buffers completions that arrived for a scheduler level other than
# the one currently waiting. The host's __agent_wait_any returns *any* finished
# agent from a single shared queue, but each parallel/pipeline level only knows
# its own tokens. Without this buffer a nested parallel would consume (and then
# mis-route) a token belonging to an outer level — corrupting the outer loop and
# deadlocking it. With it, a level that pulls a foreign token stashes the result
# here and keeps waiting; the level that actually owns the token finds it here
# before blocking. This makes parallel/pipeline safely re-entrant (nestable).
$__wf_ready = {}

# __take_for drains agent completions until one whose token is in `pending`
# arrives, returning [token, result]. Completions for other levels are stashed
# in $__wf_ready (and consumed from the host immediately, so host state is freed
# in finish order regardless of which level owns the token). Raises on cancel.
def __take_for(pending)
  pending.each_key do |t|
    return [t, $__wf_ready.delete(t)] if $__wf_ready.key?(t)
  end
  loop do
    token = __agent_wait_any
    raise "workflow: canceled" if token == 0    # cancellation sentinel
    result = __agent_take(token)
    return [token, result] if pending.key?(token)
    $__wf_ready[token] = result                 # belongs to an outer level
  end
end

# agent(prompt, opts = {}) runs one sub-agent to completion and returns its
# reply string. Inside parallel/pipeline it starts the work then yields its
# fiber, so siblings run concurrently. At top level it starts then blocks for
# its own result.
#
# opts (all optional):
#   model:      String  — override the model for this one sub-agent
#   tools:      Array   — restrict the child to this subset of tool names
#   read_only:  true    — strip the mutating tools (write_file/edit_file)
#   schema:     String  — a JSON Schema (as a JSON string) the reply must match;
#                         agent() then returns the sub-agent's reply as a JSON
#                         string — wrap in JSON.parse(...) to get native Ruby
#   isolation:  "worktree" — run the sub-agent in a fresh git worktree so its
#                         file/terminal changes don't touch the main checkout;
#                         changes are left on a branch (named in the reply)
def agent(prompt, opts = {})
  model = (opts[:model] || opts["model"]).to_s
  tools = opts[:tools] || opts["tools"] || []
  tools = tools.join(",") if tools.is_a?(Array)
  read_only = (opts[:read_only] || opts["read_only"]) ? 1 : 0
  schema = (opts[:schema] || opts["schema"]).to_s
  isolation = (opts[:isolation] || opts["isolation"]).to_s
  token = __agent_start(prompt.to_s, model, tools.to_s, read_only, schema, isolation)
  raise "workflow: token budget exhausted" if token < 0
  if $__wf_sched
    Fiber.yield(token)
  else
    # Top level: no fiber to yield, so block for this one token. Route through
    # __take_for so a stray completion from an unwound parallel (rescued mid-run)
    # can't be mistaken for our result — we wait specifically for `token`.
    _, result = __take_for({ token => true })
    result
  end
end

# skill(name, params = {}, opts = {}) runs one skill to completion and returns
# its declared outputs as a native Ruby Hash (parsed from the skill's outputs
# JSON). It dispatches by name: a recorded browser skill is replayed
# deterministically; a SKILL.md skill runs as a sub-agent. Like agent(), inside
# parallel/pipeline it starts the work then yields its fiber; at top level it
# blocks for its own result — so skill() composes in the same pipelines.
#
#   params:  Hash    — values for the skill's declared params / a recording's
#                      {{placeholders}}
#   opts:
#     schema: String — a JSON Schema (as a JSON string) a SKILL.md skill's reply
#                      must satisfy (ignored by browser recordings, whose outputs
#                      are structurally bound)
#
# Prefix the name with "browser:" or "md:" to disambiguate when a recording and
# a SKILL.md skill share a name.
def skill(name, params = {}, opts = {})
  params_json = JSON.generate(params || {})
  schema = (opts[:schema] || opts["schema"]).to_s
  token = __skill_start(name.to_s, params_json, schema)
  raise "workflow: token budget exhausted" if token < 0
  raw = if $__wf_sched
          Fiber.yield(token)
        else
          _, result = __take_for({ token => true })
          result
        end
  # Outputs always marshal to at least "{}", so an empty result only occurs when
  # the host dropped the call (cancel/skip) — treat as no outputs.
  return {} if raw.nil? || raw.empty?
  # Outputs always cross as valid JSON; a skill failure arrives as an error
  # string instead, so a parse failure means the skill failed — raise it (halting
  # the pipeline) rather than feeding a malformed value downstream.
  begin
    JSON.parse(raw)
  rescue
    raise "workflow: skill #{name} failed: #{raw}"
  end
end

# log(msg) surfaces a progress line to the user (host event), returns nil.
def log(msg)
  __log(msg.to_s)
  nil
end

# budget_remaining returns the remaining output-token budget (a large number
# when no budget was set).
def budget_remaining
  __budget_remaining
end

# args returns the workflow's input value — whatever the caller passed as the
# workflow tool's `args` — parsed from JSON into native Ruby (Hash / Array /
# scalar). Returns nil when no args were supplied. Memoized: the host call and
# JSON.parse run once. Use it to parameterize a saved/named workflow, e.g.
#   target = args["target"]
def args
  @__wf_args ||= begin
    s = __args
    (s.nil? || s.empty?) ? nil : JSON.parse(s)
  end
end

# phase(title) marks the start of a named stage so a multi-stage run reads as
# grouped steps in the progress stream instead of a flat log. It is purely
# cosmetic — scheduling is unaffected; agent/parallel/pipeline behave the same
# whether or not phases are declared. Returns nil.
def phase(title)
  __log("== phase: #{title}")
  nil
end

# __resume_branch resumes one branch's fiber and, if the block raises, re-raises
# with the failing item's index and a short preview — so a parallel/pipeline
# failure names WHICH item blew up ("item #3 ([\"strict\", \"gpt-4o\"]) failed:
# ...") instead of surfacing a bare, contextless "script error: <msg>" that
# forces the author to guess which of N branches produced it. Signals the
# scheduler itself raises — cancellation, budget exhaustion, or an inner level's
# already-localized item error — begin with "workflow: " and pass through
# unwrapped, so cancellation stays clean and nesting doesn't double-prefix.
def __resume_branch(fibers, i, items, *args)
  fibers[i].resume(*args)
rescue => e
  raise if e.message.index("workflow: ") == 0
  preview = (items[i].to_s rescue "?")
  preview = preview[0, 200] + "..." if preview.length > 200
  raise "workflow: item ##{i} (#{preview}) failed: #{e.message}"
end

# __run_fibers is the cooperative event loop: every item runs in its own fiber;
# all branches are advanced to their first agent() call (so every job is in
# flight) before any result is awaited; then completions are drained in finish
# order and the matching fiber is resumed. A fiber may yield again (e.g. a
# pipeline's second stage) — it is simply re-registered.
def __run_fibers(items)
  prev = $__wf_sched
  $__wf_sched = true
  begin
    fibers  = items.map { |it| Fiber.new { yield it } }
    results = Array.new(fibers.size)
    pending = {}                                 # host token => fiber index
    fibers.each_with_index do |f, i|
      r = __resume_branch(fibers, i, items)
      f.alive? ? (pending[r] = i) : (results[i] = r)
    end
    until pending.empty?
      token, result = __take_for(pending)         # next completion this level owns
      i      = pending.delete(token)
      f      = fibers[i]
      r      = __resume_branch(fibers, i, items, result) # agent() returns; fiber continues
      f.alive? ? (pending[r] = i) : (results[i] = r)
    end
    results
  ensure
    $__wf_sched = prev
  end
end

# parallel(items) { |item| ... } runs the block for every item concurrently and
# returns the array of block results (order matches items).
def parallel(items, &blk)
  __run_fibers(items, &blk)
end

# pipeline(items, stage1, stage2, ...) runs each item through all stages. Items
# flow independently with no barrier between stages — item A may be in stage 2
# while item B is still in stage 1. Each stage is a callable taking the previous
# stage's result.
def pipeline(items, *stages)
  __run_fibers(items) do |item|
    acc = item
    stages.each { |stage| acc = stage.call(acc) }
    acc
  end
end

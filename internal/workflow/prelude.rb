# Workflow DSL prelude — prepended to every user script by internal/workflow.
# Defines the primitives (agent / parallel / pipeline / log / budget_remaining)
# on top of the host imports (__agent_*) the wasm runtime binds. Lives in Go
# (go:embed) so the DSL can evolve without rebuilding mruby.wasm.

# $__wf_sched is true only while the cooperative event loop is driving fibers.
# It lets agent() pick the right path: yield cooperatively inside parallel/
# pipeline, but run synchronously when called at top level (no fiber to yield).
$__wf_sched = false

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
#                         agent() then returns the sub-agent's JSON as a string
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
    tok = __agent_wait_any
    raise "workflow: canceled" if tok == 0
    __agent_take(tok)
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

# phase(title) marks the start of a named stage so a multi-stage run reads as
# grouped steps in the progress stream instead of a flat log. It is purely
# cosmetic — scheduling is unaffected; agent/parallel/pipeline behave the same
# whether or not phases are declared. Returns nil.
def phase(title)
  __log("== phase: #{title}")
  nil
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
      r = f.resume
      f.alive? ? (pending[r] = i) : (results[i] = r)
    end
    until pending.empty?
      token = __agent_wait_any                   # blocks until some agent finishes
      raise "workflow: canceled" if token == 0   # cancellation sentinel
      i      = pending.delete(token)
      result = __agent_take(token)
      f      = fibers[i]
      r      = f.resume(result)                   # agent() returns; fiber continues
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

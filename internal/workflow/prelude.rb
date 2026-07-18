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

# __finish_skill_token is the shared tail of skill()/recording(): budget check,
# cooperative-or-blocking wait, and outputs parsing.
def __finish_skill_token(token, name)
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

# skill(name, params = {}, opts = {}) runs one SKILL.md skill as a sub-agent and
# returns its result as native Ruby (parsed JSON; with opts[:schema], the
# schema-constrained object). Use it when the step must follow a named skill
# exactly — the skill body is resolved and injected host-side, never left to
# the sub-agent's discretion. For free-form agentic work, call agent() instead.
#
#   params:  Hash    — inputs handed to the skill
#   opts:
#     schema: String — a JSON Schema (as a JSON string) the reply must satisfy
#
# Legacy: skill("browser:<name>") and an unprefixed name that only exists as a
# recording still replay that recording for one release — both are deprecated;
# use recording() for those. (The "md:" prefix remains accepted but is no
# longer needed: skill() only resolves SKILL.md skills now.)
def skill(name, params = {}, opts = {})
  name = name.to_s
  if name.start_with?("browser:")
    log("workflow: skill(\"#{name}\") is deprecated — use recording(\"#{name[8..-1]}\")")
  end
  params_json = JSON.generate(params || {})
  schema = (opts[:schema] || opts["schema"]).to_s
  token = __skill_start(name, params_json, schema)
  __finish_skill_token(token, name)
end

# recording(name, params = {}) replays one browser recording deterministically
# and returns its declared outputs as a native Ruby Hash (parsed from the
# outputs JSON). Compose it like skill(): inside parallel/pipeline it starts
# the work then yields its fiber; at top level it blocks for its own result.
#
#   params: Hash — values for the recording's declared {{placeholders}}
def recording(name, params = {})
  name = name.to_s
  token = __skill_start("recording:#{name}", JSON.generate(params || {}), "")
  __finish_skill_token(token, name)
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
  # Re-raise `e` explicitly (not a bare `raise`): in this mruby build a bare
  # re-raise inside a method-level rescue loses the exception message, leaving
  # only the class name — which would swallow "workflow: token budget
  # exhausted", "workflow: skill … failed", and an inner level's already-
  # localized "workflow: item #…" message.
  raise e if e.message.index("workflow: ") == 0
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

# ─── Regexp — backed by the host RE2 engine (__regex_* in runtime.c/.go) ─────
# mruby core has no Regexp; a literal /.../ compiles to Regexp.compile(src, opts)
# (mruby codegen, NODE_REGX), so defining Regexp here makes literals work. The
# engine is Go's RE2: linear-time (a model-authored pattern can't ReDoS the
# sandbox) but with NO backreferences or lookaround in the PATTERN. Replacement
# backrefs (\1, \&, \k<name>) in sub/gsub DO work — expanded here in Ruby, not by
# the engine. Everything derives from one host call, __regex_scan, which returns
# every match (BYTE offsets + captured substrings; mruby strings are byte-indexed
# without MRB_UTF8_STRING, so byte offsets keep slicing consistent) so Ruby's
# line-anchored ^/$ stay correct.

class RegexpError < StandardError; end

class Regexp
  IGNORECASE = 1
  EXTENDED   = 2
  MULTILINE  = 4

  def self.compile(source, opts = nil, enc = nil)
    new(source, opts, enc)
  end

  # Escape RE2 metacharacters in a literal string (Regexp.escape / quote).
  def self.escape(str)
    str = str.to_s
    meta = "\\^$.|?*+()[]{}"
    out = ""
    i = 0
    while i < str.length
      c = str[i, 1]
      out << "\\" if meta.include?(c)
      out << c
      i += 1
    end
    out
  end
  class << self; alias quote escape; end

  attr_reader :source

  def initialize(source, opts = nil, _enc = nil)
    @source = source.to_s
    @flags  = __normalize_flags(opts)
    err = __regex_compile_check(@source, @flags)
    raise RegexpError, err unless err.empty?
  end

  # flags string actually sent to the host ("i"/"m"/… subset); internal.
  def __flags; @flags; end

  def match(str, pos = 0)
    return nil if str.nil?
    s = str.to_s
    start = pos < 0 ? s.length + pos : pos
    return nil if start < 0 || start > s.length
    __matches(s).each { |md| return md if md.begin(0) >= start }
    nil
  end

  def match?(str, pos = 0)
    !match(str, pos).nil?
  end

  def =~(str)
    m = match(str)
    m && m.begin(0)
  end

  def ===(str)
    str.is_a?(String) && match?(str)
  end

  def to_s;    "/#{@source}/#{@flags}"; end
  def inspect; to_s; end

  # __matches returns an Array<MatchData> for every match in s (possibly empty).
  # The single point that crosses to the host engine.
  def __matches(s)
    json = __regex_scan(@source, @flags, s)
    return [] if json.empty?
    data  = JSON.parse(json)
    names = data["names"] || {}
    (data["m"] || []).map { |groups| MatchData.new(s, groups, names) }
  end

  def __normalize_flags(opts)
    return "" if opts.nil? || opts == false
    return opts if opts.is_a?(String) # a literal /.../im passes "im"
    return "i"  if opts == true       # Regexp.new(str, true) => case-insensitive
    if opts.is_a?(Integer)
      f = ""
      f += "i" if (opts & IGNORECASE) != 0
      f += "m" if (opts & MULTILINE) != 0
      f += "x" if (opts & EXTENDED) != 0
      return f
    end
    ""
  end
end

class MatchData
  # groups: [[cstart,cend,"str"] | nil, …] (index 0 = whole match); names: {name=>idx}.
  def initialize(str, groups, names)
    @string = str
    @groups = groups.map { |g| g && [g[0].to_i, g[1].to_i, g[2]] }
    @names  = names
  end

  def [](key)
    idx = (key.is_a?(String) || key.is_a?(Symbol)) ? @names[key.to_s] : key
    return nil if idx.nil?
    g = @groups[idx]
    g && g[2]
  end

  def begin(i); g = @groups[i]; g && g[0]; end
  def end(i);   g = @groups[i]; g && g[1]; end
  def pre_match;  @string[0, @groups[0][0]]; end
  def post_match; @string[@groups[0][1]..-1] || ""; end
  def to_a;       @groups.map { |g| g && g[2] }; end
  def captures;   a = to_a; a[1..-1] || []; end
  def size;       @groups.size; end
  def length;     @groups.size; end
  def string;     @string; end
  def to_s;       g = @groups[0]; g ? g[2] : ""; end

  def named_captures
    h = {}
    @names.each { |name, idx| g = @groups[idx]; h[name] = g && g[2] }
    h
  end
end

class String
  def =~(re)
    re.is_a?(Regexp) ? (re =~ self) : nil
  end

  def match(re, pos = 0)
    re = Regexp.new(re) unless re.is_a?(Regexp)
    re.match(self, pos)
  end

  def match?(re, pos = 0)
    re = Regexp.new(re) unless re.is_a?(Regexp)
    re.match?(self, pos)
  end

  # scan(re) -> Array (no block) or self (with block). Each element is the whole
  # match when the pattern has no groups, else an array of its captures.
  def scan(re)
    re = Regexp.new(re) unless re.is_a?(Regexp)
    items = re.__matches(self).map do |m|
      caps = m.captures
      caps.empty? ? m[0] : caps
    end
    return items unless block_given?
    items.each { |it| yield it }
    self
  end

  def sub(re, repl = nil, &blk)
    __gsub_common(re, repl, false, &blk)
  end

  def gsub(re, repl = nil, &blk)
    __gsub_common(re, repl, true, &blk)
  end

  def __gsub_common(re, repl, global)
    re = Regexp.new(re) unless re.is_a?(Regexp)
    matches = re.__matches(self)
    return self.dup if matches.empty?
    out  = ""
    last = 0
    matches.each do |m|
      bs = m.begin(0); es = m.end(0)
      out << self[last...bs]
      if block_given?
        out << yield(m[0]).to_s
      elsif repl.is_a?(Hash)
        out << (repl[m[0]] || "").to_s
      else
        out << __expand_repl(repl.to_s, m)
      end
      last = es
      break unless global
    end
    out << (self[last..-1] || "")
    out
  end

  # __expand_repl handles replacement backreferences (\0-\9, \&, \k<name>, \\).
  # RE2 has none in the pattern, but the replacement string is ours to expand.
  def __expand_repl(repl, m)
    out = ""
    i = 0
    n = repl.length
    while i < n
      c = repl[i, 1]
      if c == "\\" && i + 1 < n
        nx = repl[i + 1, 1]
        if nx >= "0" && nx <= "9"
          out << (m[nx.to_i] || ""); i += 2; next
        elsif nx == "&"
          out << (m[0] || ""); i += 2; next
        elsif nx == "\\"
          out << "\\"; i += 2; next
        elsif nx == "k" && i + 2 < n && repl[i + 2, 1] == "<"
          close = repl.index(">", i + 3)
          if close
            out << (m[repl[(i + 3)...close]] || ""); i = close + 1; next
          end
        end
      end
      out << c
      i += 1
    end
    out
  end

  alias __orig_split split
  def split(pat = nil, lim = 0)
    return __orig_split(pat, lim) unless pat.is_a?(Regexp)
    result = []
    fields = 0     # split fields only (limit counts these, not captures)
    last   = 0
    pat.__matches(self).each do |m|
      bs = m.begin(0); es = m.end(0)
      next if es == bs && bs == last # skip a zero-width match at the cursor
      break if lim > 0 && fields == lim - 1
      result << self[last...bs]
      fields += 1
      # Ruby appends the separator's captured groups after each field.
      (1...m.size).each { |gi| v = m[gi]; result << v unless v.nil? }
      last = es
    end
    result << (self[last..-1] || "")
    result.pop while lim == 0 && !result.empty? && result[-1] == ""
    result
  end

  alias __orig_aref []
  def [](*args)
    if args[0].is_a?(Regexp)
      m = args[0].match(self)
      return nil if m.nil?
      return m[args[1] || 0]
    end
    __orig_aref(*args)
  end
end

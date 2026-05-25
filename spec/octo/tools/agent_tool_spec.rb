# frozen_string_literal: true

RSpec.describe Octo::Tools::Agent do
  let(:tool) { described_class.new }

  describe "tool metadata" do
    it "registers under wire name 'agent'" do
      expect(described_class.tool_name).to eq("agent")
    end

    it "advertises description, prompt as required parameters" do
      expect(described_class.tool_parameters[:required]).to match_array(%w[description prompt])
    end

    it "lives in the subagent category" do
      expect(described_class.tool_category).to eq("subagent")
    end
  end

  describe "#execute argument validation" do
    it "errors when no agent is injected" do
      expect(tool.execute(description: "x", prompt: "y")).to eq({ error: "Agent context is required" })
    end

    it "errors on blank description" do
      agent = instance_double(Octo::Agent)
      expect(tool.execute(description: "  ", prompt: "y", agent: agent)).to eq({ error: "description is required" })
    end

    it "errors on blank prompt" do
      agent = instance_double(Octo::Agent)
      expect(tool.execute(description: "x", prompt: "", agent: agent)).to eq({ error: "prompt is required" })
    end
  end

  describe "#execute success path" do
    let(:registry) do
      reg = Octo::ToolRegistry.new
      [Octo::Tools::FileReader, Octo::Tools::Grep, Octo::Tools::Write,
       Octo::Tools::Edit, Octo::Tools::Terminal, described_class].each do |klass|
        instance = klass == Octo::Tools::Terminal ? klass.new(agent_session_id: "test") : klass.new
        reg.register(instance)
      end
      reg
    end

    let(:subagent) do
      sub = instance_double(Octo::Agent,
        iterations: 4,
        current_model_info: { model: "claude-haiku-4-5" },
        session_token_totals: {
          prompt_tokens: 100,
          completion_tokens: 50,
          cache_creation_input_tokens: 0,
          cache_read_input_tokens: 0
        },
        session_cost_usd: 0.0012
      )
      allow(sub).to receive(:run).and_return(status: :success)
      sub
    end

    let(:agent) do
      a = instance_double(Octo::Agent,
        ui: nil,
        session_token_totals: {
          prompt_tokens: 200,
          completion_tokens: 100,
          cache_creation_input_tokens: 0,
          cache_read_input_tokens: 0
        },
        session_cost_usd: 0.005
      )
      a.instance_variable_set(:@tool_registry, registry)
      allow(a).to receive(:instance_variable_get).with(:@tool_registry).and_return(registry)
      allow(a).to receive(:instance_variable_set)
      allow(a).to receive(:fork_subagent).and_return(subagent)
      allow(a).to receive(:send).with(:generate_subagent_summary, subagent).and_return("done!")
      allow(a).to receive(:absorb_subagent_session_usage!)
      a
    end

    it "forks with the lite model and runs the prompt" do
      expect(agent).to receive(:fork_subagent).with(
        model: "lite",
        forbidden_tools: ["agent"],
        system_prompt_suffix: a_string_including("do research")
      ).and_return(subagent)

      result = tool.execute(description: "do research", prompt: "explore feature X", agent: agent)
      expect(result).to eq("done!")
    end

    it "blocks recursion by default (forbidden_tools includes 'agent')" do
      captured_forbidden = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured_forbidden = kw[:forbidden_tools]
        subagent
      end

      tool.execute(description: "x", prompt: "y", agent: agent)
      expect(captured_forbidden).to include("agent")
    end

    it "merges caller-provided forbidden_tools with the default self-block" do
      captured_forbidden = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured_forbidden = kw[:forbidden_tools]
        subagent
      end

      tool.execute(description: "x", prompt: "y", agent: agent,
                   forbidden_tools: ["terminal", "edit"])
      expect(captured_forbidden).to include("agent", "terminal", "edit")
    end

    it "converts tools allowlist into a denylist of everything else" do
      captured_forbidden = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured_forbidden = kw[:forbidden_tools]
        subagent
      end

      tool.execute(description: "x", prompt: "y", agent: agent,
                   tools: ["file_reader", "grep"])
      # Should forbid every registered tool except file_reader and grep,
      # plus the always-on 'agent' self-block.
      expect(captured_forbidden).to include("write", "edit", "terminal", "agent")
      expect(captured_forbidden).not_to include("file_reader", "grep")
    end

    it "asks the parent to absorb the sub-agent's session usage" do
      expect(agent).to receive(:absorb_subagent_session_usage!).with(subagent)
      tool.execute(description: "x", prompt: "y", agent: agent)
    end

    it "passes a unique model when caller specifies one" do
      expect(agent).to receive(:fork_subagent).with(
        hash_including(model: "claude-opus-4-1")
      ).and_return(subagent)

      tool.execute(description: "x", prompt: "y", agent: agent, model: "claude-opus-4-1")
    end

    it "rejects unknown subagent_type with a friendly error" do
      result = tool.execute(description: "x", prompt: "y", agent: agent, subagent_type: "no-such-preset")
      expect(result).to eq({ error: "Unknown subagent_type: no-such-preset" })
    end

    it "applies a known preset's model and forbidden_tools" do
      captured = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured = kw
        subagent
      end

      tool.execute(description: "x", prompt: "y", agent: agent, subagent_type: "explore")

      # explore preset says model: lite and forbids write/edit/web_search/web_fetch/browser/agent
      expect(captured[:model]).to eq("lite")
      expect(captured[:forbidden_tools]).to include("write", "edit", "web_search", "web_fetch", "browser", "agent")
    end

    it "merges preset forbidden_tools with caller-provided ones" do
      captured_forbidden = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured_forbidden = kw[:forbidden_tools]
        subagent
      end

      tool.execute(description: "x", prompt: "y", agent: agent,
                   subagent_type: "explore", forbidden_tools: ["terminal"])

      expect(captured_forbidden).to include("terminal")          # caller
      expect(captured_forbidden).to include("write", "edit")     # preset
      expect(captured_forbidden).to include("agent")             # self-block
    end

    it "caller's explicit model overrides the preset's model" do
      captured = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured = kw
        subagent
      end

      tool.execute(description: "x", prompt: "y", agent: agent,
                   subagent_type: "explore", model: "claude-opus-4-1")

      expect(captured[:model]).to eq("claude-opus-4-1")
    end

    it "prepends the preset's system_prompt to the brief" do
      captured_suffix = nil
      allow(agent).to receive(:fork_subagent) do |**kw|
        captured_suffix = kw[:system_prompt_suffix]
        subagent
      end

      tool.execute(description: "audit auth", prompt: "y", agent: agent, subagent_type: "explore")

      expect(captured_suffix).to include("Code Exploration Sub-agent") # from explore preset
      expect(captured_suffix).to include("audit auth")                  # from brief
    end

    it "lets AgentInterrupted bubble up" do
      allow(subagent).to receive(:run).and_raise(Octo::AgentInterrupted)
      expect {
        tool.execute(description: "x", prompt: "y", agent: agent)
      }.to raise_error(Octo::AgentInterrupted)
    end
  end

  describe "#format_call" do
    it "renders the description in parens" do
      expect(tool.format_call(description: "investigate auth")).to eq("Agent(investigate auth)")
    end
  end

  describe "#format_result" do
    it "prefixes errors" do
      expect(tool.format_result({ error: "nope" })).to eq("Error: nope")
    end

    it "shows the first line of a string result" do
      expect(tool.format_result("Done in 4 iterations\nDetails...")).to eq("Done in 4 iterations")
    end
  end
end

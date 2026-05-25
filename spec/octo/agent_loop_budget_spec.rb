# frozen_string_literal: true

RSpec.describe Octo::Agent, "loop budget gates" do
  let(:client) do
    instance_double(Octo::Client).tap do |c|
      c.instance_variable_set(:@api_key, "test-api-key")
    end
  end

  def build_agent(max_turns: nil, max_cost_usd: nil)
    config = Octo::AgentConfig.new(
      permission_mode: :auto_approve,
      max_turns: max_turns,
      max_cost_usd: max_cost_usd
    )
    config.add_model(
      model: "claude-sonnet-4-5",
      api_key: "test-api-key",
      base_url: "https://api.anthropic.com"
    )
    described_class.new(
      client, config,
      working_dir: Dir.pwd,
      ui: nil,
      profile: "coding",
      session_id: Octo::SessionManager.generate_id,
      source: :manual
    )
  end

  describe "AgentConfig" do
    it "defaults max_turns to 30 and max_cost_usd to nil" do
      cfg = Octo::AgentConfig.new
      expect(cfg.max_turns).to eq(30)
      expect(cfg.max_cost_usd).to be_nil
    end

    it "honors explicit nil max_turns (no limit)" do
      cfg = Octo::AgentConfig.new(max_turns: nil)
      expect(cfg.max_turns).to be_nil
    end

    it "persists max_turns and max_cost_usd through to_yaml" do
      cfg = Octo::AgentConfig.new(max_turns: 10, max_cost_usd: 2.5)
      yaml = cfg.to_yaml
      expect(yaml).to include("max_turns: 10")
      expect(yaml).to include("max_cost_usd: 2.5")
    end
  end

  describe "#accumulate_session_usage!" do
    it "rolls token counts and USD cost into session totals" do
      agent = build_agent
      agent.send(:accumulate_session_usage!, {
        prompt_tokens: 100_000,
        completion_tokens: 50_000,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0
      })

      expect(agent.session_token_totals[:prompt_tokens]).to eq(100_000)
      expect(agent.session_token_totals[:completion_tokens]).to eq(50_000)
      # Sonnet 4.5 default tier: $3 input + $15 output per 1M.
      # 100k @ $3 + 50k @ $15 = $0.30 + $0.75 = $1.05
      expect(agent.session_cost_usd).to be_within(0.001).of(1.05)
    end

    it "leaves cost at 0 when the model is not priced" do
      agent = build_agent
      allow(agent).to receive(:current_model_name_for_pricing).and_return("self-hosted-llama")
      agent.send(:accumulate_session_usage!, {
        prompt_tokens: 1_000_000,
        completion_tokens: 1_000_000
      })
      expect(agent.session_cost_usd).to eq(0.0)
      expect(agent.session_token_totals[:prompt_tokens]).to eq(1_000_000)
    end
  end

  describe "#absorb_subagent_session_usage!" do
    it "adds the sub-agent's token and cost totals into the parent" do
      parent = build_agent
      sub = build_agent
      parent.send(:accumulate_session_usage!, {
        prompt_tokens: 100_000, completion_tokens: 50_000
      })
      sub.send(:accumulate_session_usage!, {
        prompt_tokens: 200_000, completion_tokens: 100_000
      })
      parent_cost_before = parent.session_cost_usd
      sub_cost = sub.session_cost_usd

      parent.absorb_subagent_session_usage!(sub)

      expect(parent.session_token_totals[:prompt_tokens]).to eq(300_000)
      expect(parent.session_token_totals[:completion_tokens]).to eq(150_000)
      expect(parent.session_cost_usd).to be_within(0.0001).of(parent_cost_before + sub_cost)
    end

    it "is a no-op for nil sub-agent" do
      parent = build_agent
      parent.send(:accumulate_session_usage!, { prompt_tokens: 100, completion_tokens: 50 })
      expect { parent.absorb_subagent_session_usage!(nil) }.not_to raise_error
      expect(parent.session_token_totals[:prompt_tokens]).to eq(100)
    end
  end

  describe "#enforce_loop_budget!" do
    it "raises TurnLimitExceeded once task turns exceed the budget" do
      agent = build_agent(max_turns: 3)
      agent.instance_variable_set(:@task_start_iterations, 0)
      agent.instance_variable_set(:@iterations, 4)
      expect {
        agent.send(:enforce_loop_budget!)
      }.to raise_error(Octo::TurnLimitExceeded, /max_turns/)
    end

    it "stays silent when within the turn budget" do
      agent = build_agent(max_turns: 3)
      agent.instance_variable_set(:@task_start_iterations, 0)
      agent.instance_variable_set(:@iterations, 3)
      expect { agent.send(:enforce_loop_budget!) }.not_to raise_error
    end

    it "raises CostLimitExceeded once session cost passes the budget" do
      agent = build_agent(max_cost_usd: 0.10)
      agent.instance_variable_set(:@session_cost_usd, 0.25)
      expect {
        agent.send(:enforce_loop_budget!)
      }.to raise_error(Octo::CostLimitExceeded, /\$0\.25/)
    end

    it "does not gate cost when max_cost_usd is unset" do
      agent = build_agent
      agent.instance_variable_set(:@session_cost_usd, 99.0)
      expect { agent.send(:enforce_loop_budget!) }.not_to raise_error
    end
  end
end

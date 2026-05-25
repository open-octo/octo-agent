# frozen_string_literal: true

require "tmpdir"
require "fileutils"

RSpec.describe Octo::SubagentRegistry do
  before { described_class.reset! }

  describe ".find" do
    it "loads the four built-in presets" do
      %w[explore plan verification general-purpose].each do |name|
        preset = described_class.find(name)
        expect(preset).to be_a(Octo::SubagentPreset), "missing built-in preset: #{name}"
        expect(preset.name).to eq(name)
      end
    end

    it "returns nil for unknown presets" do
      expect(described_class.find("not-a-real-preset")).to be_nil
    end

    it "returns nil for blank name" do
      expect(described_class.find(nil)).to be_nil
      expect(described_class.find("")).to be_nil
    end

    it "memoizes lookups" do
      first = described_class.find("explore")
      second = described_class.find("explore")
      expect(first).to equal(second)
    end

    it "prefers the user-override dir over the built-in" do
      Dir.mktmpdir do |tmp|
        user_explore = File.join(tmp, "explore")
        FileUtils.mkdir_p(user_explore)
        File.write(File.join(user_explore, "subagent.yml"), <<~YAML)
          description: User-overridden explore
          model: claude-opus-4-1
        YAML
        File.write(File.join(user_explore, "system_prompt.md"), "USER OVERRIDE")

        stub_const("Octo::SubagentRegistry::USER_DIR", tmp)
        described_class.reset!

        preset = described_class.find("explore")
        expect(preset.model).to eq("claude-opus-4-1")
        expect(preset.system_prompt).to eq("USER OVERRIDE")
      end
    end
  end

  describe "built-in preset metadata" do
    it "exposes explore's read-only constraints" do
      preset = described_class.find("explore")
      expect(preset.model).to eq("lite")
      expect(preset.forbidden_tools).to include("write", "edit", "browser", "agent")
      expect(preset.system_prompt).to include("Code Exploration Sub-agent")
    end

    it "exposes plan's no-execution constraints" do
      preset = described_class.find("plan")
      expect(preset.forbidden_tools).to include("write", "edit", "terminal", "agent")
    end

    it "exposes verification's read-plus-test constraints" do
      preset = described_class.find("verification")
      expect(preset.forbidden_tools).to include("write", "edit", "agent")
      # Importantly: terminal is NOT forbidden — verification runs tests.
      expect(preset.forbidden_tools).not_to include("terminal")
    end

    it "exposes general-purpose's only-no-recursion constraint" do
      preset = described_class.find("general-purpose")
      expect(preset.forbidden_tools).to eq(["agent"])
    end
  end
end

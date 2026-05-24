# frozen_string_literal: true

require "spec_helper"
require "clacky/ui2/view_renderer"

RSpec.describe Clacky::UI2::ViewRenderer do
  let(:renderer) { described_class.new }

  describe "#render_user_message" do
    it "renders user message with symbol" do
      result = renderer.render_user_message("Hello")
      expect(result).to include("[>>]")
      expect(result).to include("Hello")
    end

    it "includes timestamp when provided" do
      time = Time.now
      result = renderer.render_user_message("Hello", timestamp: time)
      expect(result).to match(/\[\d{2}:\d{2}:\d{2}\]/)
    end
  end

  describe "#render_assistant_message" do
    it "renders assistant message with symbol" do
      result = renderer.render_assistant_message("World")
      expect(result).to include("[<<]")
      expect(result).to include("World")
    end

    it "returns empty string for nil content" do
      result = renderer.render_assistant_message(nil)
      expect(result).to eq("")
    end
  end

  describe "#render_tool_call" do
    it "renders tool call with name and description" do
      result = renderer.render_tool_call(
        tool_name: "file_reader",
        formatted_call: "file_reader(path: 'test.rb')"
      )
      expect(result).to include("[=>]")
      expect(result).to include("file_reader")
    end
  end

  describe "#render_tool_result" do
    it "renders tool result" do
      result = renderer.render_tool_result(result: "Success")
      expect(result).to include("[<=]")
      expect(result).to include("Success")
    end
  end

  describe "#render_tool_error" do
    it "renders tool error in low-key style (same symbol as tool_result)" do
      result = renderer.render_tool_error(error: "File not found")
      # Uses tool_result symbol [<=] instead of [XX] to avoid alarming users
      # for non-critical errors that the agent can retry
      expect(result).to include("[<=]")
      expect(result).to include("File not found")
    end
  end

  describe "#render_thinking" do
    it "renders thinking indicator" do
      result = renderer.render_thinking
      expect(result).to include("[..]")
      expect(result).to include("Thinking")
    end
  end

  describe "#render_success" do
    it "renders success message" do
      result = renderer.render_success("Operation completed")
      expect(result).to include("[OK]")
      expect(result).to include("Operation completed")
    end
  end

  describe "#render_error" do
    it "renders error message" do
      result = renderer.render_error("Something went wrong")
      expect(result).to include("[ER]")
      expect(result).to include("Something went wrong")
    end
  end

  describe "#render_warning" do
    it "renders warning message" do
      result = renderer.render_warning("Be careful")
      expect(result).to include("[!!]")
      expect(result).to include("Be careful")
    end
  end

end

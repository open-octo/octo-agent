# frozen_string_literal: true

# Tests for get_recent_messages_with_tool_pairs
# Verifies tool_call / tool_result pairing is preserved during compression
# for both Anthropic format (role:user + content:[{type:tool_result}])
# and OpenAI format (role:tool + tool_call_id).

RSpec.describe "get_recent_messages_with_tool_pairs" do
  # Minimal stub that includes only the helper under test
  let(:helper) do
    Class.new do
      include Clacky::Agent::MessageCompressorHelper
    end.new
  end

  def call(messages, count)
    helper.get_recent_messages_with_tool_pairs(messages, count)
  end

  # ── helpers to build fixture messages ────────────────────────────────────────

  def user_msg(text)
    { role: "user", content: text }
  end

  def assistant_msg(text, tool_calls: nil)
    m = { role: "assistant", content: text }
    m[:tool_calls] = tool_calls if tool_calls
    m
  end

  # Anthropic-style tool result: packed into a user message
  def anthropic_tool_result(tool_use_id, content = "ok")
    {
      role: "user",
      content: [{ type: "tool_result", tool_use_id: tool_use_id, content: content }]
    }
  end

  # OpenAI-style tool result: separate role:tool message
  def openai_tool_result(tool_call_id, content = "ok")
    { role: "tool", tool_call_id: tool_call_id, content: content }
  end

  def tool_call(id, name = "write")
    { id: id, function: { name: name } }
  end

  # ── OpenAI format ─────────────────────────────────────────────────────────────

  describe "OpenAI format (role: tool)" do
    it "keeps assistant+tool_result together when cutting at that boundary" do
      messages = [
        user_msg("hello"),
        assistant_msg("thinking", tool_calls: [tool_call("id1")]),
        openai_tool_result("id1"),
        assistant_msg("done")
      ]

      # Request only 1 message — lands on the last assistant
      # but the preceding pair should NOT be split if we request 2
      result = call(messages, 2)
      roles = result.map { |m| m[:role] }

      # Must not include assistant(tool_calls) without its tool result
      assistant_with_calls = result.find { |m| m[:role] == "assistant" && m[:tool_calls] }
      if assistant_with_calls
        call_ids = assistant_with_calls[:tool_calls].map { |tc| tc[:id] }
        result_ids = result.select { |m| m[:role] == "tool" }.map { |m| m[:tool_call_id] }
        expect(result_ids).to include(*call_ids), "OpenAI: tool result missing for assistant with tool_calls"
      end
    end

    it "keeps tool result with its assistant when the tool result is the boundary message" do
      messages = [
        user_msg("a"),
        assistant_msg("step1", tool_calls: [tool_call("id1")]),
        openai_tool_result("id1"),
        user_msg("b"),
        assistant_msg("step2", tool_calls: [tool_call("id2")]),
        openai_tool_result("id2")
      ]

      # Ask for 1 — gets last tool result, must also pull its assistant
      result = call(messages, 1)
      assistant_present = result.any? { |m| m[:role] == "assistant" && m[:tool_calls]&.any? { |tc| tc[:id] == "id2" } }
      tool_present = result.any? { |m| m[:role] == "tool" && m[:tool_call_id] == "id2" }

      expect(assistant_present).to be(true), "OpenAI: assistant missing when tool result is boundary"
      expect(tool_present).to be(true)
    end
  end

  # ── Anthropic format ──────────────────────────────────────────────────────────

  describe "Anthropic format (role: user with tool_result content)" do
    it "keeps assistant+tool_result together when the result message is a user message" do
      messages = [
        user_msg("hello"),
        assistant_msg("thinking", tool_calls: [tool_call("id1")]),
        anthropic_tool_result("id1"),
        assistant_msg("done")
      ]

      # Request 2 messages — expect to get last assistant + maybe more,
      # but if the assistant-with-tool_calls is included, its tool_result MUST also be included
      result = call(messages, 2)

      assistant_with_calls = result.find { |m| m[:role] == "assistant" && m[:tool_calls] }
      if assistant_with_calls
        call_ids = assistant_with_calls[:tool_calls].map { |tc| tc[:id] }
        # The corresponding tool_result should be in a user message with type:tool_result
        paired = result.any? do |m|
          m[:role] == "user" &&
            m[:content].is_a?(Array) &&
            m[:content].any? { |b| b[:type] == "tool_result" && call_ids.include?(b[:tool_use_id]) }
        end
        expect(paired).to be(true), "Anthropic: tool_result user message missing for assistant with tool_calls"
      end
    end

    it "keeps tool_result user message with its assistant when it is the boundary message" do
      messages = [
        user_msg("a"),
        assistant_msg("step1", tool_calls: [tool_call("id1")]),
        anthropic_tool_result("id1"),
        user_msg("b"),
        assistant_msg("step2", tool_calls: [tool_call("id2")]),
        anthropic_tool_result("id2")
      ]

      # Ask for 1 — boundary is the last anthropic_tool_result (role:user)
      result = call(messages, 1)

      # The last tool_result user message should be included
      tool_result_present = result.any? do |m|
        m[:role] == "user" &&
          m[:content].is_a?(Array) &&
          m[:content].any? { |b| b[:type] == "tool_result" && b[:tool_use_id] == "id2" }
      end

      # Its paired assistant should also be included
      assistant_present = result.any? do |m|
        m[:role] == "assistant" && m[:tool_calls]&.any? { |tc| tc[:id] == "id2" }
      end

      expect(tool_result_present).to be(true), "Anthropic: tool_result user message should be in result"
      expect(assistant_present).to be(true), "Anthropic: assistant missing when anthropic tool_result is boundary — BUG!"
    end
  end

  # ── Edge cases ────────────────────────────────────────────────────────────────

  describe "edge cases" do
    it "returns [] for empty messages" do
      expect(call([], 5)).to eq([])
    end

    it "returns all messages when count >= length" do
      messages = [user_msg("a"), assistant_msg("b")]
      expect(call(messages, 10).length).to eq(2)
    end

    it "preserves original message order" do
      messages = [
        user_msg("1"),
        assistant_msg("2", tool_calls: [tool_call("id1")]),
        openai_tool_result("id1"),
        assistant_msg("3")
      ]
      result = call(messages, 4)
      expect(result.map { |m| m[:content] }).to eq(["1", "2", "ok", "3"])
    end
  end
end

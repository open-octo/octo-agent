# frozen_string_literal: true

require "spec_helper"
# spec_helper already loads the full clacky gem, which defines:
#   Clacky::BadRequestError, Clacky::RetryableError, Clacky::AgentError
require "clacky/agent/llm_caller"

# Unit tests for the helper detector methods inside Clacky::Agent::LlmCaller.
#
# Both detectors are pure functions of the error object (no instance state),
# so we test them via a minimal host class that mixes in the module and
# exposes the private methods. This avoids the heavy mocking required to
# spin up a full Agent instance just to read a string.
RSpec.describe Clacky::Agent::LlmCaller do
  let(:host_class) do
    Class.new do
      include Clacky::Agent::LlmCaller
      # Re-expose the private helpers we want to exercise.
      public :context_too_long_error?, :reasoning_content_missing_error?
    end
  end
  let(:host) { host_class.new }

  describe "#context_too_long_error?" do
    # ── REAL production error strings observed across providers ──────────
    # When adding a new provider, paste the actual upstream message here so
    # regressions are caught next time someone tweaks the matcher.
    PROVIDER_ERRORS = {
      "OpenAI classic" =>
        "[LLM] Client request error: This model's maximum context length " \
        "is 128000 tokens. However, your messages resulted in 130456 tokens. " \
        "Please reduce the length of the messages.",

      "OpenAI error.code field" =>
        "[LLM] Client request error: error.code=context_length_exceeded the request was too large",

      "Anthropic numeric pattern" =>
        "[LLM] Client request error: prompt is too long: 218849 tokens > 200000 maximum",

      "Anthropic-compat relay 'input is too long'" =>
        "[LLM] Client request error: input is too long for this model",

      # The exact customer-reported error from the screenshot.
      "Qwen / Alibaba DashScope (customer report)" =>
        "[LLM] Client request error: You passed 117345 input tokens and " \
        "requested 8192 output tokens. However the model's context length " \
        "is only 125536 tokens, resulting in a maximum input length of " \
        "117344 tokens. Please reduce the length of the input prompt. " \
        "(parameter=input_tokens, value=117345)",

      # Verified live against qwen3.6-27b on 2026-05-09 (probe_context_overflow.rb).
      # DashScope's newer terse error format used by qwen3.x series.
      "Qwen / Alibaba DashScope (qwen3.6 terse format)" =>
        "[LLM] Client request error: <400> InternalError.Algo.InvalidParameter: " \
        "Range of input length should be [1, 229376]",

      "Generic gateway (Portkey / OpenRouter)" =>
        "[LLM] Client request error: The total number of tokens exceeds " \
        "the model's maximum context length",

      "DeepSeek (OpenAI-compatible)" =>
        "[LLM] Client request error: This model's maximum context length " \
        "is 65536 tokens. Your input has 80000 tokens.",

      "Kimi 'input length exceeds'" =>
        "[LLM] Client request error: input length exceeds maximum context length: 200000"
    }.freeze

    PROVIDER_ERRORS.each do |label, raw_message|
      it "detects: #{label}" do
        err = Clacky::BadRequestError.new(raw_message)
        expect(host.context_too_long_error?(err)).to be(true),
          "Expected to match this error message but didn't:\n  #{raw_message.inspect}"
      end
    end

    # ── Negative tests: errors that are NOT context-too-long ─────────────
    # These must NOT trigger the compression-and-retry path, since doing so
    # would waste an LLM call (acceptable but undesirable). All of these
    # are 400 errors that should be left to propagate as-is.
    context "with unrelated 400 errors (must not match)" do
      it "rejects an auth-related 400 mentioning 'token' (auth token, not context)" do
        err = Clacky::BadRequestError.new(
          "[LLM] Client request error: invalid auth token"
        )
        expect(host.context_too_long_error?(err)).to be false
      end

      it "rejects a malformed-tool-args error" do
        err = Clacky::BadRequestError.new(
          "[LLM] Client request error: tool_calls[0].arguments is not valid JSON"
        )
        expect(host.context_too_long_error?(err)).to be false
      end

      it "rejects a missing-field error" do
        err = Clacky::BadRequestError.new(
          "[LLM] Client request error: messages[3].role is required"
        )
        expect(host.context_too_long_error?(err)).to be false
      end

      it "rejects a generic 'parameter is invalid' error" do
        err = Clacky::BadRequestError.new(
          "[LLM] Client request error: parameter=temperature is invalid"
        )
        expect(host.context_too_long_error?(err)).to be false
      end

      it "rejects a 'file path too long' filesystem error (mentions long but not prompt/context)" do
        err = Clacky::BadRequestError.new(
          "[LLM] Client request error: the file path is too long"
        )
        expect(host.context_too_long_error?(err)).to be false
      end
    end

    # ── Type guard ────────────────────────────────────────────────────────
    # The matcher must only ever return true for BadRequestError. A 5xx that
    # happens to mention "context length" in passing must not trigger our
    # one-shot compression retry — that path belongs solely to true 400s.
    context "with non-BadRequestError inputs" do
      it "returns false for a plain StandardError" do
        expect(host.context_too_long_error?(StandardError.new("context length exceeded"))).to be false
      end

      it "returns false for a RetryableError even if the message would match" do
        err = Clacky::RetryableError.new("context length exceeded")
        expect(host.context_too_long_error?(err)).to be false
      end

      it "returns false for nil" do
        expect(host.context_too_long_error?(nil)).to be false
      end
    end

    # ── Edge / robustness ─────────────────────────────────────────────────
    context "robustness" do
      it "is case-insensitive" do
        err = Clacky::BadRequestError.new(
          "PROMPT IS TOO LONG: 9999 TOKENS > 8000 MAXIMUM"
        )
        expect(host.context_too_long_error?(err)).to be true
      end

      it "tolerates extra whitespace inside the numeric Anthropic pattern" do
        err = Clacky::BadRequestError.new(
          "[LLM] something something 42  tokens   >   40 maximum"
        )
        expect(host.context_too_long_error?(err)).to be true
      end

      it "handles an empty error message safely" do
        expect(host.context_too_long_error?(Clacky::BadRequestError.new(""))).to be false
      end
    end
  end

  # ── Sanity check that the existing detector still works (regression guard) ──
  describe "#reasoning_content_missing_error?" do
    it "still detects DeepSeek/Kimi thinking-mode reasoning_content errors" do
      err = Clacky::BadRequestError.new(
        "[LLM] Client request error: reasoning_content must be passed back when thinking is enabled"
      )
      expect(host.reasoning_content_missing_error?(err)).to be true
    end

    it "does not confuse a context-too-long error for a reasoning_content error" do
      err = Clacky::BadRequestError.new(
        "[LLM] Client request error: prompt is too long: 218849 tokens > 200000 maximum"
      )
      expect(host.reasoning_content_missing_error?(err)).to be false
    end
  end
end

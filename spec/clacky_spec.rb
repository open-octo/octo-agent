# frozen_string_literal: true

RSpec.describe Clacky do
  it "has a version number" do
    expect(Clacky::VERSION).not_to be nil
  end

  it "defines the main module" do
    expect(Clacky).to be_a(Module)
  end

  it "defines the AgentError class" do
    expect(Clacky::AgentError).to be < StandardError
  end

  it "defines the ToolCallError class" do
    expect(Clacky::ToolCallError).to be < Clacky::AgentError
  end
end

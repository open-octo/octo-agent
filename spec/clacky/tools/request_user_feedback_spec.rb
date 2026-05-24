# frozen_string_literal: true

RSpec.describe Clacky::Tools::RequestUserFeedback do
  let(:tool) { described_class.new }

  describe "#execute" do
    it "returns formatted message with question only" do
      result = tool.execute(question: "What color scheme should I use?")

      expect(result[:success]).to be true
      expect(result[:message]).to include("**Question:** What color scheme should I use?")
      expect(result[:awaiting_feedback]).to be true
    end

    it "includes context when provided" do
      result = tool.execute(
        question: "Should I use SQLite or PostgreSQL?",
        context: "I need to choose a database for the project"
      )

      expect(result[:message]).to include("**Context:** I need to choose a database for the project")
      expect(result[:message]).to include("**Question:** Should I use SQLite or PostgreSQL?")
      expect(result[:awaiting_feedback]).to be true
    end

    it "includes options when provided" do
      result = tool.execute(
        question: "Which framework should I use?",
        options: ["Rails", "Sinatra", "Hanami"]
      )

      expect(result[:message]).to include("**Options:**")
      expect(result[:message]).to include("1. Rails")
      expect(result[:message]).to include("2. Sinatra")
      expect(result[:message]).to include("3. Hanami")
      expect(result[:awaiting_feedback]).to be true
    end

    it "includes all elements when provided" do
      result = tool.execute(
        question: "Which approach is better?",
        context: "I need to decide on the architecture pattern",
        options: ["MVC", "Microservices", "Serverless"]
      )

      expect(result[:message]).to include("**Context:**")
      expect(result[:message]).to include("**Question:**")
      expect(result[:message]).to include("**Options:**")
      expect(result[:awaiting_feedback]).to be true
    end

    it "handles empty context gracefully" do
      result = tool.execute(
        question: "What should I do?",
        context: ""
      )

      expect(result[:message]).not_to include("**Context:**")
      expect(result[:message]).to include("**Question:** What should I do?")
    end

    it "handles empty options array" do
      result = tool.execute(
        question: "What should I do?",
        options: []
      )

      expect(result[:message]).not_to include("**Options:**")
      expect(result[:message]).to include("**Question:** What should I do?")
    end
  end

  describe "#format_call" do
    it "shows preview of short question" do
      args = { question: "What color?" }
      formatted = tool.format_call(args)

      expect(formatted).to eq('request_user_feedback("What color?")')
    end

    it "truncates long question" do
      long_question = "A" * 100
      args = { question: long_question }
      formatted = tool.format_call(args)

      expect(formatted).to include("request_user_feedback")
      expect(formatted.length).to be < long_question.length + 50
      expect(formatted).to include("...")
    end

    it "handles string keys" do
      args = { "question" => "What color?" }
      formatted = tool.format_call(args)

      expect(formatted).to eq('request_user_feedback("What color?")')
    end
  end

  describe "#format_result" do
    it "returns the formatted message" do
      result = { message: "**Question:** Test question", awaiting_feedback: true }
      formatted = tool.format_result(result)

      expect(formatted).to eq("**Question:** Test question")
    end

    it "handles non-hash result" do
      result = "some string"
      formatted = tool.format_result(result)

      expect(formatted).to eq("Waiting for user feedback...")
    end
  end

  describe "#to_function_definition" do
    it "returns OpenAI function calling format" do
      definition = tool.to_function_definition

      expect(definition[:type]).to eq("function")
      expect(definition[:function][:name]).to eq("request_user_feedback")
      expect(definition[:function][:parameters][:type]).to eq("object")
    end

    it "has question as required parameter" do
      definition = tool.to_function_definition
      required = definition[:function][:parameters][:required]

      expect(required).to include("question")
    end

    it "has context and options as optional parameters" do
      definition = tool.to_function_definition
      properties = definition[:function][:parameters][:properties]

      expect(properties).to have_key(:question)
      expect(properties).to have_key(:context)
      expect(properties).to have_key(:options)
    end

    it "defines options as array of strings" do
      definition = tool.to_function_definition
      options_def = definition[:function][:parameters][:properties][:options]

      expect(options_def[:type]).to eq("array")
      expect(options_def[:items][:type]).to eq("string")
    end
  end
end

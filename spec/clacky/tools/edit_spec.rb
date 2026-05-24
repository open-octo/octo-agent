# frozen_string_literal: true

require "tempfile"
require "tmpdir"

RSpec.describe Clacky::Tools::Edit do
  let(:tool) { described_class.new }

  describe "#execute" do
    it "replaces string in file" do
      Dir.mktmpdir do |dir|
        file_path = File.join(dir, "test.txt")
        File.write(file_path, "Hello, World!")

        result = tool.execute(
          path: file_path,
          old_string: "World",
          new_string: "Ruby"
        )

        expect(result[:error]).to be_nil
        expect(result[:replacements]).to eq(1)
        expect(File.read(file_path)).to eq("Hello, Ruby!")
      end
    end

    it "replaces all occurrences when replace_all is true" do
      Dir.mktmpdir do |dir|
        file_path = File.join(dir, "test.txt")
        File.write(file_path, "foo bar foo baz foo")

        result = tool.execute(
          path: file_path,
          old_string: "foo",
          new_string: "qux",
          replace_all: true
        )

        expect(result[:error]).to be_nil
        expect(result[:replacements]).to eq(3)
        expect(File.read(file_path)).to eq("qux bar qux baz qux")
      end
    end

    it "returns error when string not found" do
      Dir.mktmpdir do |dir|
        file_path = File.join(dir, "test.txt")
        File.write(file_path, "Hello, World!")

        result = tool.execute(
          path: file_path,
          old_string: "notfound",
          new_string: "replacement"
        )

        expect(result[:error]).to include("not found")
      end
    end

    it "returns error for file not found" do
      result = tool.execute(
        path: "/nonexistent/file.txt",
        old_string: "foo",
        new_string: "bar"
      )

      expect(result[:error]).to include("not found")
    end

    it "returns error for ambiguous replacement without replace_all" do
      Dir.mktmpdir do |dir|
        file_path = File.join(dir, "test.txt")
        File.write(file_path, "foo foo foo")

        result = tool.execute(
          path: file_path,
          old_string: "foo",
          new_string: "bar",
          replace_all: false
        )

        expect(result[:error]).to include("appears 3 times")
      end
    end

    it "preserves whitespace and indentation" do
      Dir.mktmpdir do |dir|
        file_path = File.join(dir, "test.txt")
        original = "  def hello\n    puts 'world'\n  end"
        File.write(file_path, original)

        result = tool.execute(
          path: file_path,
          old_string: "    puts 'world'",
          new_string: "    puts 'Ruby'"
        )

        expect(result[:error]).to be_nil
        expect(File.read(file_path)).to eq("  def hello\n    puts 'Ruby'\n  end")
      end
    end

    context "smart whitespace matching" do
      it "matches content with different leading whitespace (tabs vs spaces)" do
        Dir.mktmpdir do |dir|
          file_path = File.join(dir, "test.rb")
          # File has 4 spaces
          File.write(file_path, "def test\n    puts 'hello'\nend")

          # AI provides tabs instead of spaces in old_string
          result = tool.execute(
            path: file_path,
            old_string: "def test\n\tputs 'hello'\nend",
            new_string: "def test\n    puts 'world'\nend"
          )

          expect(result[:error]).to be_nil
          expect(result[:replacements]).to eq(1)
          expect(File.read(file_path)).to eq("def test\n    puts 'world'\nend")
        end
      end

      it "matches content when indentation uses mixed spaces/tabs" do
        Dir.mktmpdir do |dir|
          file_path = File.join(dir, "test.rb")
          # File has 2 spaces for first level, 4 spaces for second level
          File.write(file_path, "class Foo\n  def bar\n    'baz'\n  end\nend")

          # AI provides tab instead of 4 spaces (but same logical indentation structure)
          result = tool.execute(
            path: file_path,
            old_string: "  def bar\n\t'baz'\n  end\n",
            new_string: "  def bar\n    'qux'\n  end\n"
          )

          expect(result[:error]).to be_nil
          expect(result[:replacements]).to eq(1)
          # Should preserve original indentation
          expect(File.read(file_path)).to eq("class Foo\n  def bar\n    'qux'\n  end\nend")
        end
      end

      it "provides helpful error when first line matches but full string doesn't" do
        Dir.mktmpdir do |dir|
          file_path = File.join(dir, "test.rb")
          File.write(file_path, "def hello\n  puts 'world'\n  puts 'ruby'\nend")

          result = tool.execute(
            path: file_path,
            old_string: "def hello\n  puts 'world'\n  puts 'python'",
            new_string: "something else"
          )

          expect(result[:error]).to include("line 1")
          expect(result[:error]).to include("whitespace differences")
          expect(result[:error]).to include("TIP")
        end
      end

      it "provides helpful error with context when string not found" do
        Dir.mktmpdir do |dir|
          file_path = File.join(dir, "test.rb")
          File.write(file_path, "def hello\n  puts 'world'\nend")

          result = tool.execute(
            path: file_path,
            old_string: "def goodbye\n  puts 'world'\nend",
            new_string: "something else"
          )

          expect(result[:error]).to include("not found")
          expect(result[:error]).to include("TIP")
          expect(result[:error]).to include("file_reader")
        end
      end
    end
  end

  describe "#to_function_definition" do
    it "returns OpenAI function calling format" do
      definition = tool.to_function_definition

      expect(definition[:type]).to eq("function")
      expect(definition[:function][:name]).to eq("edit")
      expect(definition[:function][:description]).to be_a(String)
      expect(definition[:function][:parameters][:required]).to include("path")
      expect(definition[:function][:parameters][:required]).to include("old_string")
      expect(definition[:function][:parameters][:required]).to include("new_string")
    end
  end
end

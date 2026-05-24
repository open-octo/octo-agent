# frozen_string_literal: true

require "tmpdir"
require "fileutils"
require "pathname"

RSpec.describe Clacky::CLI do
  describe "working directory validation" do
    let(:cli) { Clacky::CLI.new }

    it "uses current directory when no path is specified" do
      result = cli.send(:validate_working_directory, nil)
      expect(result).to eq(Dir.pwd)
    end

    it "expands relative paths to absolute paths" do
      Dir.mktmpdir do |dir|
        Dir.chdir(dir) do
          FileUtils.mkdir_p("subdir")
          result = cli.send(:validate_working_directory, "subdir")
          expected = Pathname.new(File.join(dir, "subdir")).realpath.to_s
          expect(Pathname.new(result).realpath.to_s).to eq(expected)
        end
      end
    end

    it "validates that the path exists" do
      expect do
        cli.send(:validate_working_directory, "/nonexistent/path")
      end.to raise_error(SystemExit)
    end

    it "validates that the path is a directory" do
      Dir.mktmpdir do |dir|
        file_path = File.join(dir, "file.txt")
        File.write(file_path, "test")

        expect do
          cli.send(:validate_working_directory, file_path)
        end.to raise_error(SystemExit)
      end
    end
  end
end

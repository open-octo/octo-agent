# frozen_string_literal: true

module Clacky
  module Utils
    # Discovers .clackyrules files in the workspace.
    # Used by both SystemPromptBuilder (rules injection) and WelcomeBanner (UI display).
    module WorkspaceRules
      RULES_FILENAMES = [".clackyrules", ".cursorrules", "CLAUDE.md"].freeze
      SUB_PROJECT_SUMMARY_LINES = 5

      # Find the main rules file in the given directory.
      # @param dir [String] Directory to search
      # @return [Hash, nil] { path:, name:, content: } or nil
      def self.find_main(dir)
        RULES_FILENAMES.each do |filename|
          path = File.join(dir, filename)
          next unless File.exist?(path)

          content = File.read(path).strip
          return { path: path, name: filename, content: content } unless content.empty?
        end
        nil
      end

      # Find all sub-project .clackyrules in immediate subdirectories.
      # @param dir [String] Parent directory to scan
      # @return [Array<Hash>] Array of { sub_name:, relative_path:, content:, summary: }
      def self.find_sub_projects(dir)
        Dir.glob(File.join(dir, "*", ".clackyrules")).sort.filter_map do |rules_path|
          content = File.read(rules_path).strip
          next if content.empty?

          sub_name = File.basename(File.dirname(rules_path))
          summary = content.lines.first(SUB_PROJECT_SUMMARY_LINES).map(&:chomp).join("\n")

          {
            sub_name: sub_name,
            relative_path: "#{sub_name}/.clackyrules",
            content: content,
            summary: summary
          }
        end
      end
    end
  end
end

# frozen_string_literal: true

module Octo
  # Discovery + cache for SubagentPreset directories.
  #
  # Lookup order for `find("name")`:
  #   1. ~/.octo/subagents/<name>/    (user override)
  #   2. <gem>/lib/octo/default_subagents/<name>/  (built-in)
  #
  # Returns nil when the name isn't found anywhere — callers (notably the
  # `agent` tool) treat that as "no preset, use raw caller-provided params".
  module SubagentRegistry
    DEFAULT_DIR = File.expand_path("../default_subagents", __FILE__).freeze
    USER_DIR    = File.expand_path("~/.octo/subagents").freeze

    class << self
      # @param name [String, Symbol]
      # @return [Octo::SubagentPreset, nil]
      def find(name)
        return nil if name.nil? || name.to_s.empty?
        cache[name.to_s] ||= resolve(name.to_s)
      end

      # @return [Array<String>] preset names available in either directory
      def names
        (Dir.children(USER_DIR).select { |d| Dir.exist?(File.join(USER_DIR, d)) } rescue []) +
          (Dir.children(DEFAULT_DIR).select { |d| Dir.exist?(File.join(DEFAULT_DIR, d)) } rescue [])
      end

      # Drop the in-memory cache. Mainly for tests; the registry is read-once
      # in normal use because preset files don't change at runtime.
      def reset!
        @cache = nil
      end

      def cache
        @cache ||= {}
      end

      def resolve(name)
        user_dir    = File.join(USER_DIR, name)
        default_dir = File.join(DEFAULT_DIR, name)

        if Dir.exist?(user_dir)
          SubagentPreset.from_dir(user_dir)
        elsif Dir.exist?(default_dir)
          SubagentPreset.from_dir(default_dir)
        end
      end
    end
  end
end

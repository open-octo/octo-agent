# frozen_string_literal: true

require "yaml"

module Octo
  # A named, packaged "AI persona" used as a forked sub-agent. Each preset
  # lives in its own directory containing:
  #
  #   subagent.yml     — metadata (model, forbidden_tools, description)
  #   system_prompt.md — curated instructions prepended into the sub-agent's
  #                      cloned history as a system_prompt_suffix
  #
  # Presets are looked up via Octo::SubagentRegistry by name and consumed by
  # `Octo::Tools::Agent` when the LLM passes `subagent_type:`. They are also
  # consumable directly by Ruby code paths (MemoryUpdater, recall flows) once
  # the existing built-in skills are migrated over.
  #
  # @example
  #   preset = Octo::SubagentRegistry.find("explore")
  #   preset.model            # => "lite"
  #   preset.forbidden_tools  # => ["write", "edit"]
  #   preset.system_prompt    # => "You are a code exploration sub-agent..."
  class SubagentPreset
    attr_reader :name, :description, :model, :forbidden_tools

    # Build a preset from a directory containing subagent.yml + system_prompt.md.
    # Missing files are tolerated (description / system_prompt default to empty).
    #
    # @param dir [String] absolute path to the preset directory
    # @return [SubagentPreset, nil] nil if the directory does not exist
    def self.from_dir(dir)
      return nil unless Dir.exist?(dir)
      new(dir)
    end

    def initialize(dir)
      @dir = dir
      @name = File.basename(dir)
      meta = load_meta
      @description = meta["description"].to_s
      @model = meta["model"] # nil if not set; callers fall back to their default
      @forbidden_tools = Array(meta["forbidden_tools"]).map(&:to_s)
      @system_prompt_content = nil # lazy
    end

    # @return [String] the system_prompt.md content, or empty string if absent
    def system_prompt
      @system_prompt_content ||= begin
        path = File.join(@dir, "system_prompt.md")
        File.exist?(path) ? File.read(path).strip : ""
      end
    end

    private def load_meta
      path = File.join(@dir, "subagent.yml")
      return {} unless File.exist?(path)
      YAML.safe_load(File.read(path)) || {}
    end
  end
end

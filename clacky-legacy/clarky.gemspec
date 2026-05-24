# frozen_string_literal: true

Gem::Specification.new do |spec|
  spec.name = "clarky"
  spec.version = "0.5.1"
  spec.authors = ["ClackyAI Team"]
  spec.email = ["support@clacky.ai"]

  spec.summary = "Legacy name for openclacky - AI agent command-line interface"
  spec.description = "This is a placeholder gem. Installing 'clarky' will automatically install 'openclacky'. The clarky command is maintained for backward compatibility."
  spec.homepage = "https://github.com/clacky-ai/openclacky"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.1.0"

  spec.metadata["homepage_uri"] = spec.homepage
  spec.metadata["source_code_uri"] = "https://github.com/clacky-ai/openclacky"
  spec.metadata["changelog_uri"] = "https://github.com/clacky-ai/openclacky/blob/main/CHANGELOG.md"

  spec.files = Dir["lib/**/*", "bin/*", "README.md", "LICENSE.txt"]
  spec.require_paths = ["lib"]

  # Depend on the main openclacky gem
  spec.add_dependency "openclacky", ">= 0.5.0"
end

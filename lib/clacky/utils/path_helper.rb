# frozen_string_literal: true

module Clacky
  module Utils
    module PathHelper
      # Safely get basename from path, return placeholder if path is nil
      def self.safe_basename(path, placeholder: "?")
        return placeholder if path.nil? || path.to_s.empty?
        File.basename(path.to_s)
      rescue StandardError
        placeholder
      end
    end
  end
end

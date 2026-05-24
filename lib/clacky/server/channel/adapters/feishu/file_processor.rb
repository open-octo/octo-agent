# frozen_string_literal: true

require "clacky/utils/file_processor"

module Clacky
  module Channel
    module Adapters
      module Feishu
        # Processes file attachments downloaded from Feishu messages.
        # Delegates to the unified Clacky::Utils::FileProcessor pipeline:
        #   - saves original file to disk
        #   - generates structured Markdown preview for Office/ZIP files
        #   - returns a formatted prompt snippet describing both paths
        module FileProcessor
          MAX_FILE_BYTES = Clacky::Utils::FileProcessor::MAX_FILE_BYTES

          # Process a downloaded file and return a text snippet for the agent prompt.
          # @param body      [String] Raw file bytes
          # @param file_name [String] Original file name
          # @return [String] Text to inject into the prompt
          def self.process(body, file_name)
            if body.bytesize > MAX_FILE_BYTES
              return "[Attachment: #{file_name}]\nFile too large " \
                     "(#{body.bytesize / 1024 / 1024}MB, max #{MAX_FILE_BYTES / 1024 / 1024}MB)."
            end

            ref = Clacky::Utils::FileProcessor.process(body: body, filename: file_name)
            ref.to_prompt
          rescue => e
            "[Attachment: #{file_name}]\n(Processing failed: #{e.message})"
          end
        end
      end
    end
  end
end

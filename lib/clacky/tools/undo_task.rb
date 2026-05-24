# frozen_string_literal: true

module Clacky
  module Tools
    # Tool for undoing the last task (Time Machine feature)
    class UndoTask < Base
      self.tool_name = "undo_task"
      self.tool_description = "Undo the last task and restore files to previous state. " \
        "Use when user wants to go back to previous state or undo recent changes."
      self.tool_category = "time_machine"
      self.tool_parameters = {
        type: "object",
        properties: {}
      }

      def execute(agent:, **_args)
        result = agent.undo_last_task
        
        if result[:success]
          result[:message]
        else
          "Error: #{result[:message]}"
        end
      end

      def format_call(**_args)
        "Undoing last task..."
      end

      def format_result(result)
        result
      end
    end
  end
end

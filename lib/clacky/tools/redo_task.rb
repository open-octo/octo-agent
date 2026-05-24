# frozen_string_literal: true

module Clacky
  module Tools
    # Tool for redoing a task after undo (Time Machine feature)
    class RedoTask < Base
      self.tool_name = "redo_task"
      self.tool_description = "Redo to a specific task after undo. Restores files to that task's state. " \
        "Use when user wants to go forward to a future task or switch to a different branch."
      self.tool_category = "time_machine"
      self.tool_parameters = {
        type: "object",
        properties: {
          task_id: {
            type: "integer",
            description: "The task ID to redo to (must be greater than current active task)"
          }
        },
        required: ["task_id"]
      }

      def execute(agent:, task_id:, **_args)
        result = agent.switch_to_task(task_id)
        
        if result[:success]
          result[:message]
        else
          "Error: #{result[:message]}"
        end
      end

      def format_call(task_id:, **_args)
        "Redoing to task #{task_id}..."
      end

      def format_result(result)
        result
      end
    end
  end
end

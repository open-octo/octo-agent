# frozen_string_literal: true

module Clacky
  module Tools
    # Tool for listing task history (Time Machine feature)
    class ListTasks < Base
      self.tool_name = "list_tasks"
      self.tool_description = "List recent tasks in the task history with summaries. " \
        "Shows current task, past tasks, and future tasks (after undo). " \
        "Use when user wants to see task history or choose which task to undo/redo to."
      self.tool_category = "time_machine"
      self.tool_parameters = {
        type: "object",
        properties: {
          limit: {
            type: "integer",
            description: "Maximum number of recent tasks to show (default: 10)",
            default: 10
          }
        }
      }

      def execute(agent:, limit: 10, **_args)
        history = agent.get_task_history(limit: limit)
        
        if history.empty?
          return "No task history available."
        end

        lines = ["Task History:"]
        history.each do |task|
          indicator = case task[:status]
          when :current then "→"
          when :past then " "
          when :future then "↯"
          end
          
          branch_indicator = task[:has_branches] ? " ⎇" : ""
          lines << "#{indicator}#{branch_indicator} Task #{task[:task_id]}: #{task[:summary]}"
        end

        lines.join("\n")
      end

      def format_call(limit: 10, **_args)
        "Listing task history (limit: #{limit})..."
      end

      def format_result(result)
        result
      end
    end
  end
end

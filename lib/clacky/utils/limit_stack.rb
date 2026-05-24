# frozen_string_literal: true

module Clacky
  module Utils
    # Auto-rolling fixed-size array.
    # Automatically discards oldest elements when the line-count limit is exceeded.
    #
    # Optional limits (all default to nil = no limit):
    #   max_line_chars – truncate each individual line to this many characters on push
    #   max_chars      – once the total accepted chars reach this threshold, further
    #                    pushes are silently dropped (sets #truncated? = true)
    #
    # These extra limits are fully opt-in; existing callers that only pass max_size
    # are completely unaffected.
    class LimitStack
      attr_reader :max_size, :items

      def initialize(max_size: 5000, max_line_chars: nil, max_chars: nil)
        @max_size       = max_size
        @max_line_chars = max_line_chars
        @max_chars      = max_chars

        @items          = []
        @total_chars    = 0   # chars currently stored in @items
        @truncated      = false
        @chars_full     = false  # latched true once max_chars is reached
      end

      # True if any content was dropped (lines rolled off the front OR
      # chars budget was exceeded OR a line was truncated).
      def truncated?
        @truncated
      end

      # Add elements (supports single or multiple)
      def push(*elements)
        elements.each do |element|
          _push_one(element)
        end
        self
      end
      alias_method :<<, :push

      # Add multi-line text (split by lines and add)
      def push_lines(text)
        return self if text.nil? || text.empty?

        lines = text.is_a?(Array) ? text : text.lines
        lines.each { |line| _push_one(line) }
        self
      end

      # Remove and return the last element
      def pop
        item = @items.pop
        @total_chars -= item.length if item.is_a?(String)
        item
      end

      # Get last N elements
      def last(n = nil)
        n ? @items.last(n) : @items.last
      end

      # Get all elements
      def to_a
        @items.dup
      end

      # Convert to string (for text content)
      def to_s
        @items.join
      end

      # Current size
      def size
        @items.size
      end

      # Check if empty
      def empty?
        @items.empty?
      end

      # Clear all elements
      def clear
        @items.clear
        @total_chars = 0
        @truncated   = false
        @chars_full  = false
        self
      end

      # Iterate over elements
      def each(&block)
        @items.each(&block)
      end

      # kept for compatibility (called internally; public so subclasses can override)
      def trim_if_needed
        while @items.size > @max_size
          removed = @items.shift
          @total_chars -= removed.length if removed.is_a?(String)
          @truncated = true
        end
      end

      private def _push_one(element)
        # --- chars budget check ---
        if @chars_full
          @truncated = true
          return
        end

        item = element

        # --- per-line truncation ---
        if @max_line_chars && item.is_a?(String) && item.length > @max_line_chars
          item = item[0, @max_line_chars]
          # Preserve trailing newline if original had one
          item += "\n" if element.end_with?("\n") && !item.end_with?("\n")
          @truncated = true
        end

        # --- total chars check ---
        if @max_chars && item.is_a?(String)
          remaining = @max_chars - @total_chars
          if remaining <= 0
            @chars_full = true
            @truncated  = true
            return
          end
          if item.length > remaining
            # If original line ends with \n we must preserve it, so reserve 1
            # byte for it — this keeps total_chars strictly within max_chars.
            needs_newline = element.is_a?(String) && element.end_with?("\n")
            cut = needs_newline ? [remaining - 1, 0].max : remaining
            item = item[0, cut]
            item += "\n" if needs_newline && !item.end_with?("\n")
            @chars_full = true
            @truncated  = true
          end
        end

        @items << item
        @total_chars += item.length if item.is_a?(String)

        trim_if_needed
      end
    end
  end
end

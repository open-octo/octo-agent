# frozen_string_literal: true

require_relative "../utils/limit_stack"

module Clacky
  module UI2
    # OutputBuffer manages the logical sequence of rendered output lines.
    #
    # It replaces the scattered state that used to live across
    # LayoutManager (@output_buffer + @output_row) and UIController
    # (@progress_message / "last line" assumptions).
    #
    # Core concepts:
    #
    # - Every append returns an +id+. Callers can later +replace(id, ...)+
    #   or +remove(id)+ that exact entry without relying on "the last line".
    # - Each entry tracks whether it has been "committed" to the terminal
    #   scrollback (i.e. scrolled off the top of the visible window by a
    #   native terminal \n). Committed entries are NEVER re-drawn from the
    #   buffer again — this is what prevents the classic "scroll up shows
    #   a duplicated line" bug.
    # - Entries may contain multi-line content (already wrapped). Each entry
    #   stores its visual line count so the renderer can compute exact rows
    #   to clear when replacing or removing.
    #
    # The buffer itself does NOT talk to the terminal. It is a pure data
    # structure; a renderer (LayoutManager) consumes it through the
    # snapshot APIs: +visible_entries+, +entry_by_id+, +tail_lines+.
    class OutputBuffer
      # A single logical output entry.
      #
      # @!attribute id [Integer]    Monotonic id, unique within the buffer
      # @!attribute lines [Array<String>]  Rendered (already-wrapped) visual lines
      # @!attribute kind [Symbol]   :text | :progress | :system  (hint for renderer)
      # @!attribute committed [Boolean] True once pushed into terminal scrollback
      Entry = Struct.new(:id, :lines, :kind, :committed, :committed_line_offset, keyword_init: true) do
        # Visual row count this entry currently OCCUPIES on screen. Once a
        # prefix of the entry's lines has been pushed into scrollback by
        # a scroll+partial-commit, those prefix rows are no longer on
        # screen — so height drops accordingly. When +committed+ flips to
        # true the entry is considered fully off-screen and height is 0.
        def height
          return 0 if committed
          lines.length - (committed_line_offset || 0)
        end

        # The currently on-screen lines of this entry (lines that haven't
        # been pushed to scrollback yet). Returns [] once fully committed.
        def visible_lines
          return [] if committed
          off = committed_line_offset || 0
          off.zero? ? lines : lines[off..] || []
        end

        def to_s
          lines.join("\n")
        end
      end

      DEFAULT_MAX_ENTRIES = 2000

      attr_reader :entries

      def initialize(max_entries: DEFAULT_MAX_ENTRIES)
        @entries       = []   # Array<Entry> in insertion order
        @index         = {}   # id => Entry (fast lookup)
        @next_id       = 1
        @max_entries   = max_entries
        @mutex         = Mutex.new
        # Monotonic counter incremented every time the buffer changes.
        # Renderers can compare this against a saved version to decide
        # whether their cached screen image is still valid.
        @version       = 0
      end

      # Append a new entry. +content+ may be a String (may include \n) or
      # an Array<String> of already-split lines.
      #
      # @param content [String, Array<String>]
      # @param kind [Symbol] :text (default), :progress, :system
      # @return [Integer] id of the newly created entry
      def append(content, kind: :text)
        @mutex.synchronize do
          lines = normalize_lines(content)
          entry = Entry.new(id: next_id!, lines: lines, kind: kind, committed: false, committed_line_offset: 0)
          @entries << entry
          @index[entry.id] = entry
          trim_if_needed
          bump_version
          entry.id
        end
      end

      # Replace an existing entry's content. If the id no longer exists
      # (e.g. the entry was trimmed or already committed and recycled),
      # this is a no-op and returns nil.
      #
      # Replacing a committed entry is silently ignored — committed content
      # lives in terminal scrollback and cannot be edited in place. Same
      # for an entry whose prefix has been partial-committed: the prefix
      # is already in scrollback and replacing the entry would either
      # strand those lines (if shorter) or duplicate them (if longer).
      #
      # @param id [Integer]
      # @param content [String, Array<String>]
      # @return [Integer, nil] Old visible height if replaced, nil if no-op
      def replace(id, content)
        @mutex.synchronize do
          entry = @index[id]
          return nil unless entry
          return nil if entry.committed
          return nil if (entry.committed_line_offset || 0) > 0

          old_height = entry.height
          entry.lines = normalize_lines(content)
          bump_version
          old_height
        end
      end

      # Remove an entry. Committed entries cannot be removed (they are in
      # terminal scrollback). Partially-committed entries also cannot be
      # removed — their prefix is frozen in scrollback. Returns the
      # removed Entry, or nil if no-op.
      #
      # @param id [Integer]
      # @return [Entry, nil]
      def remove(id)
        @mutex.synchronize do
          entry = @index[id]
          return nil unless entry
          return nil if entry.committed
          return nil if (entry.committed_line_offset || 0) > 0

          @entries.delete(entry)
          @index.delete(id)
          bump_version
          entry
        end
      end

      # Mark an entry (and every older live entry) as committed to terminal
      # scrollback. Called by the renderer after it has emitted a native \n
      # that scrolled the top-of-screen row off into scrollback.
      #
      # Committing always flows from oldest → newest: if entry X is
      # committed, every entry older than X must also be committed, because
      # they have already scrolled past X on the screen.
      #
      # @param id [Integer]
      def commit_through(id)
        @mutex.synchronize do
          committed_any = false
          @entries.each do |e|
            break if e.id > id
            unless e.committed
              e.committed = true
              committed_any = true
            end
          end
          bump_version if committed_any
        end
      end

      # Commit the oldest N VISUAL rows. Used when the renderer scrolls N
      # lines off the top via native \n. Commits are precise at the visual
      # row granularity (even mid-entry): if the oldest live entry is
      # multi-line and only its prefix has scrolled off, that prefix is
      # recorded in +committed_line_offset+ and only the still-visible
      # suffix remains eligible for future repaints.
      #
      # This is the critical invariant for preventing the "scroll up to
      # see a line already in scrollback, then render_output_from_buffer
      # repaints it again on screen" duplicate-output regression: every
      # visual row that went into terminal scrollback MUST be removed
      # from the buffer's pool of repaintable live rows, regardless of
      # whether it sat alone in a 1-line entry or at the top of a 10-line
      # entry.
      #
      # @param line_count [Integer] Number of visual lines pushed to scrollback
      # @return [Integer] Number of entries NEWLY marked fully committed
      #   (partial commits on an entry do NOT count toward this total —
      #   callers use the return value only as a debug hint, not for row
      #   bookkeeping).
      def commit_oldest_lines(line_count)
        return 0 if line_count <= 0

        @mutex.synchronize do
          remaining = line_count
          committed = 0
          changed   = false
          @entries.each do |e|
            break if remaining <= 0
            next if e.committed

            h = e.height
            if h <= remaining
              # Full scroll-off of this entry's remaining visible rows.
              e.committed = true
              e.committed_line_offset = e.lines.length  # normalize
              remaining -= h
              committed += 1
              changed    = true
            else
              # Partial scroll: record the new offset and stop (there are
              # still visible rows of this entry on screen).
              e.committed_line_offset = (e.committed_line_offset || 0) + remaining
              remaining = 0
              changed   = true
              break
            end
          end
          bump_version if changed
          committed
        end
      end

      # Entries that are still live (not committed). These are candidates
      # for re-rendering into the visible output area.
      #
      # @return [Array<Entry>]
      def live_entries
        @mutex.synchronize { @entries.reject(&:committed).dup }
      end

      # The last N *visual lines* across live entries, preserving entry
      # boundaries. Returns an Array<String> suitable for row-by-row
      # painting. If the last live entry is taller than +n+, only its last
      # +n+ lines are returned.
      #
      # @param n [Integer]
      # @return [Array<String>]
      def tail_lines(n)
        return [] if n <= 0

        @mutex.synchronize do
          collected = []
          @entries.reverse_each do |e|
            break if collected.length >= n
            next if e.committed

            # The entry's still-visible lines (excluding any prefix already
            # committed to scrollback via a partial commit).
            vis = e.visible_lines
            next if vis.empty?

            # Prepend the entry's visible lines in order
            remaining = n - collected.length
            if vis.length <= remaining
              collected = vis + collected
            else
              collected = vis.last(remaining) + collected
              break
            end
          end
          collected
        end
      end

      # Look up an entry by id.
      # @param id [Integer]
      # @return [Entry, nil]
      def entry_by_id(id)
        @mutex.synchronize { @index[id] }
      end

      # Does this id still refer to a live, editable entry?
      # @param id [Integer]
      def live?(id)
        @mutex.synchronize do
          e = @index[id]
          !!(e && !e.committed)
        end
      end

      # Does this id refer to an entry that can still be replaced or
      # removed in place? A partially-committed entry (prefix already in
      # scrollback via a scroll) is NOT editable — its visible suffix is
      # frozen until it either fully commits or (rare) a full repaint
      # rewrites the screen.
      #
      # @param id [Integer]
      def fully_editable?(id)
        @mutex.synchronize do
          e = @index[id]
          !!(e && !e.committed && (e.committed_line_offset || 0) == 0)
        end
      end

      # Total number of entries (committed + live) currently tracked.
      def size
        @mutex.synchronize { @entries.size }
      end

      # Number of live entries.
      def live_size
        @mutex.synchronize { @entries.count { |e| !e.committed } }
      end

      # Total visual lines across live entries.
      def live_line_count
        @mutex.synchronize { @entries.sum { |e| e.committed ? 0 : e.height } }
      end

      # Monotonic version (incremented on every mutation).
      def version
        @version
      end

      # Clear everything. Used by /clear command.
      def clear
        @mutex.synchronize do
          @entries.clear
          @index.clear
          bump_version
        end
      end

      # --- helpers ----------------------------------------------------------

      private def next_id!
        id = @next_id
        @next_id += 1
        id
      end

      private def bump_version
        @version += 1
      end

      # Drop the oldest entries when the buffer grows past the cap. This is
      # a soft safety net — in practice live entries stay small because
      # write_output_line commits them to scrollback as they scroll off.
      private def trim_if_needed
        while @entries.size > @max_entries
          dropped = @entries.shift
          @index.delete(dropped.id)
        end
      end

      # Normalize input into an array of visual lines (no trailing \n kept).
      # Empty strings are preserved so callers can explicitly append blank
      # rows.
      #
      # Rules:
      # - nil             → [""]
      # - Array<String>   → deep copy (caller has pre-split)
      # - "hello"         → ["hello"]
      # - "a\nb"          → ["a", "b"]
      # - "a\n"           → ["a"]         (trailing newline is not a new line)
      # - "a\n\n"         → ["a", ""]     (explicit blank line preserved)
      # - ""              → [""]
      private def normalize_lines(content)
        case content
        when nil
          [""]
        when Array
          content.map(&:to_s)
        else
          str = content.to_s
          return [""] if str.empty?
          # Strip a single trailing newline so "a\n" → ["a"], but keep
          # explicit blank lines ("a\n\n" → ["a", ""]).
          str = str.chomp("\n")
          str.split("\n", -1)
        end
      end
    end
  end
end

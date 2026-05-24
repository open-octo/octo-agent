# frozen_string_literal: true

require "clacky/ui2/layout_manager"

# Regression test for the "duplicated line after scroll" bug.
#
# Symptom (user-reported):
#   - During idle compression, or just after scrolling terminal viewport
#     upwards while the LLM keeps printing new output, a recently-seen
#     line appears TWICE — once in the native terminal scrollback (where
#     it scrolled off), and once again on screen (repainted from the
#     output buffer).
#
# Root cause:
#   LayoutManager#paint_new_lines scrolls by emitting `print "\n"`, which
#   pushes exactly ONE visual row from the top of the output area into
#   the terminal's native scrollback. Immediately after, it calls
#   `@buffer.commit_oldest_lines(1)` to mark that row as "committed"
#   (i.e. already in scrollback, must not be repainted from buffer).
#
#   BUT commit_oldest_lines works entry-by-entry: if the oldest live
#   entry is *multi-line* (height >= 2, e.g. a long token-usage line
#   that terminal-wrapped to 2 rows), it refuses to partially commit
#   that entry and simply breaks out, committing ZERO entries.
#
#   Consequence: the row is in scrollback but buffer still thinks the
#   entire entry is live. The next `render_output_from_buffer` (fired
#   on resize, fixed-area height change, quiet progress handle start /
#   finish during idle compression, etc.) re-paints that entry in full
#   — producing a visible duplicate of the row already sitting in
#   scrollback.
RSpec.describe Clacky::UI2::LayoutManager, "#paint_new_lines scroll / commit accounting" do
  let(:stub_input_area) do
    Class.new do
      attr_accessor :row
      def required_height; 2; end
      def paused?; false; end
      def render(start_row:, width: nil); end
      def clear_user_tip; end
    end.new
  end

  def build_manager(width: 80, height: 10)
    lm = Clacky::UI2::LayoutManager.new(input_area: stub_input_area)
    lm.screen.instance_variable_set(:@width, width)
    lm.screen.instance_variable_set(:@height, height)
    lm.send(:calculate_layout)
    lm
  end

  # Swallow $stdout writes so the test runner's terminal isn't scribbled
  # on during paint. We're asserting on buffer state, not emitted bytes.
  around(:each) do |ex|
    Kernel.send(:alias_method, :__orig_print_for_paint_commit_spec, :print)
    Kernel.send(:define_method, :print) { |*_args| }
    begin
      ex.run
    ensure
      Kernel.send(:alias_method, :print, :__orig_print_for_paint_commit_spec)
      Kernel.send(:remove_method, :__orig_print_for_paint_commit_spec)
    end
  end

  it "commits exactly one VISUAL row per scroll \\n, even when the oldest live entry spans multiple lines" do
    lm = build_manager(width: 80, height: 10)
    # Output area height for the 10-row screen:
    #   fixed = gap(1) + todo(0) + input(2) = 3  →  output_area = 7 rows

    # Entry A: a multi-line entry (height 2) at the top.
    # In production this is e.g. a 160-char token-usage line wrapping
    # twice at width 80, or any multi-line tool output.
    a_id = lm.append("A-line-1\nA-line-2")

    # Fill the rest of the output area: A takes rows 0..1 (2 lines), so
    # 5 more single-line entries bring us to output_row == 7 with the
    # screen exactly full.
    (1..5).each { |i| lm.append("single-#{i}") }
    expect(lm.instance_variable_get(:@output_row)).to eq(7),
      "sanity: should be full (7 rows: 2 from A + 5 singles)"

    # Now one more append triggers exactly one scroll — emits one \n at
    # bottom, which pushes A-line-1 into terminal scrollback.
    lm.append("trigger-scroll")

    buf = lm.instance_variable_get(:@buffer)
    a_entry = buf.entry_by_id(a_id)

    # THIS IS THE BUG:
    # Before the fix, buf.commit_oldest_lines(1) sees A.height=2 > remaining=1
    # and breaks with ZERO committed. A stays fully live in the buffer,
    # even though its first line is already in scrollback. The next
    # render_output_from_buffer would re-emit A-line-1 again.
    #
    # After the fix, the buffer's oldest-committed bookkeeping accounts for
    # A-line-1 — either by committing A whole, or by tracking a per-entry
    # line-offset. The invariant this spec asserts: the TOTAL number of
    # buffer-visible-rows that would be repainted by a full rebuild must
    # equal the output-area height (7) — not one more, not one less.

    # Expected on-screen state after the scroll:
    #   scrollback: A-line-1
    #   row 0:      A-line-2   (top of visible area)
    #   rows 1..5:  single-1 .. single-5
    #   row 6:      trigger-scroll
    #
    # So a correct buffer repaint of the live content (tail_lines(7))
    # must emit exactly these 7 rows — no A-line-1.
    tail = buf.tail_lines(7)

    expect(tail.length).to eq(7),
      "buffer should report exactly 7 live visual rows matching the " \
      "output area, but got #{tail.length}: #{tail.inspect}. " \
      "A value > 7 means commit_oldest_lines(1) failed to commit " \
      "A-line-1 after the scroll, and a future render_output_from_buffer " \
      "will duplicate it into scrollback."

    expect(tail).not_to include("A-line-1"),
      "A-line-1 was scrolled into terminal scrollback and must NOT be " \
      "eligible for a buffer repaint. Finding it in tail_lines means " \
      "the buffer still thinks A is fully live, and the next " \
      "render_output_from_buffer will duplicate it."

    expect(tail.first).to eq("A-line-2"),
      "expected the first live visible row to be the remaining half " \
      "of A (A-line-2), got #{tail.first.inspect}"
    expect(tail.last).to eq("trigger-scroll"),
      "expected the last live visible row to be trigger-scroll, got " \
      "#{tail.last.inspect}"
  end

  it "repaints from buffer produce no duplicates after a multi-line entry is partially scrolled off" do
    # End-to-end version: trigger a scroll, then force a full repaint
    # (what happens on resize or fixed-area height change), and confirm
    # the repaint doesn't re-emit the row that's already in scrollback.
    lm = build_manager(width: 80, height: 10)

    lm.append("TOP-line-1\nTOP-line-2")
    (1..5).each { |i| lm.append("row-#{i}") }
    lm.append("trigger-scroll")   # this forces exactly one \n
    # Capture what a buffer-driven repaint would emit.
    emitted = []
    Kernel.send(:alias_method, :__orig_print_for_repaint_spec, :print)
    Kernel.send(:define_method, :print) { |*args| args.each { |a| emitted << a.to_s } }
    begin
      lm.send(:render_output_from_buffer)
    ensure
      Kernel.send(:alias_method, :print, :__orig_print_for_repaint_spec)
      Kernel.send(:remove_method, :__orig_print_for_repaint_spec)
    end

    content = emitted.reject { |w| w.start_with?("\e") || w == "\n" }

    # TOP-line-1 was scrolled into native scrollback. A repaint that
    # re-emits it is the duplicate-output regression.
    expect(content).not_to include("TOP-line-1"),
      "render_output_from_buffer repainted a line that is already in " \
      "native terminal scrollback, producing a visible duplicate. " \
      "Emitted content: #{content.inspect}"

    # Sanity: the remaining half of TOP should still be live and repainted.
    expect(content).to include("TOP-line-2")
  end

  it "keeps buffer.live_line_count in sync with actual visible screen rows after many scrolls" do
    # After any sequence of appends, the buffer's live line count must
    # NEVER exceed the output area height. Every visible row above the
    # output area has been pushed to native scrollback via `\n` and
    # must correspondingly be marked committed in the buffer.
    #
    # If live_line_count > output_area_height, a later render_output_from_buffer
    # (triggered by e.g. sessionbar status change during idle compression,
    # or input height change) will ask tail_lines() for the last N lines
    # — but the "old" live lines are still there, and any bookkeeping
    # that compares against the ACTUAL top-of-screen will be off by the
    # un-committed excess. That's the duplicate-output regression the
    # user reports.
    lm = build_manager(width: 80, height: 10)
    output_height = 7  # 10 - fixed(3)

    # Append a mix of single-line and multi-line entries that together
    # force many scrolls. Multi-line entries are the critical ingredient:
    # when their first line scrolls off, commit_oldest_lines(1) must NOT
    # refuse to commit just because the entry isn't fully scrolled.
    lm.append("multi-A-line-1\nmulti-A-line-2\nmulti-A-line-3")
    lm.append("single-1")
    lm.append("multi-B-line-1\nmulti-B-line-2")
    (1..10).each { |i| lm.append("filler-#{i}") }
    lm.append("multi-C-line-1\nmulti-C-line-2")
    (1..5).each { |i| lm.append("tail-#{i}") }

    buf = lm.instance_variable_get(:@buffer)

    expect(buf.live_line_count).to be <= output_height,
      "buffer.live_line_count = #{buf.live_line_count} exceeds the " \
      "output area height (#{output_height}). Every excess live line " \
      "is a row that was pushed into scrollback by `\n` but not " \
      "committed in the buffer, which means render_output_from_buffer " \
      "will re-paint it and the user will see a duplicate of a row " \
      "they already saw scrolled off."
  end
end

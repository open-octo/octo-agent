# frozen_string_literal: true

require "clacky/ui2/output_buffer"

RSpec.describe Clacky::UI2::OutputBuffer do
  # ---------------------------------------------------------------------------
  # append + basic queries
  # ---------------------------------------------------------------------------
  describe "#append" do
    it "returns a monotonic id" do
      buf = described_class.new
      id1 = buf.append("hello")
      id2 = buf.append("world")
      expect(id2).to be > id1
    end

    it "splits multi-line strings into visual lines" do
      buf = described_class.new
      id = buf.append("a\nb\nc")
      expect(buf.entry_by_id(id).lines).to eq(["a", "b", "c"])
    end

    it "strips a single trailing newline" do
      buf = described_class.new
      id = buf.append("a\n")
      expect(buf.entry_by_id(id).lines).to eq(["a"])
    end

    it "preserves explicit blank lines" do
      buf = described_class.new
      id = buf.append("a\n\n")
      expect(buf.entry_by_id(id).lines).to eq(["a", ""])
    end

    it "treats nil / empty string as a single blank line" do
      buf = described_class.new
      id1 = buf.append(nil)
      id2 = buf.append("")
      expect(buf.entry_by_id(id1).lines).to eq([""])
      expect(buf.entry_by_id(id2).lines).to eq([""])
    end

    it "accepts pre-split Array<String> as-is" do
      buf = described_class.new
      id = buf.append(["row 1", "row 2"])
      expect(buf.entry_by_id(id).lines).to eq(["row 1", "row 2"])
    end

    it "bumps version on every append" do
      buf = described_class.new
      v0 = buf.version
      buf.append("x")
      buf.append("y")
      expect(buf.version).to eq(v0 + 2)
    end
  end

  # ---------------------------------------------------------------------------
  # replace
  # ---------------------------------------------------------------------------
  describe "#replace" do
    it "updates an existing live entry" do
      buf = described_class.new
      id = buf.append("thinking… 1s")
      buf.replace(id, "thinking… 2s")
      expect(buf.entry_by_id(id).lines).to eq(["thinking… 2s"])
    end

    it "returns the old height" do
      buf = described_class.new
      id = buf.append("one-line")
      expect(buf.replace(id, "a\nb")).to eq(1)
    end

    it "is a no-op for unknown id" do
      buf = described_class.new
      expect(buf.replace(999, "x")).to be_nil
    end

    it "is a no-op for committed entries" do
      buf = described_class.new
      id = buf.append("scrolled away")
      buf.commit_through(id)
      expect(buf.replace(id, "try to edit")).to be_nil
      # content unchanged
      expect(buf.entry_by_id(id).lines).to eq(["scrolled away"])
    end
  end

  # ---------------------------------------------------------------------------
  # remove
  # ---------------------------------------------------------------------------
  describe "#remove" do
    it "deletes a live entry" do
      buf = described_class.new
      id = buf.append("progress…")
      buf.remove(id)
      expect(buf.entry_by_id(id)).to be_nil
      expect(buf.live_entries).to be_empty
    end

    it "refuses to remove committed entries" do
      buf = described_class.new
      id = buf.append("x")
      buf.commit_through(id)
      expect(buf.remove(id)).to be_nil
      expect(buf.entry_by_id(id)).not_to be_nil
    end
  end

  # ---------------------------------------------------------------------------
  # commit — the anti-double-render invariant
  # ---------------------------------------------------------------------------
  describe "#commit_oldest_lines" do
    it "marks whole entries committed greedily from the oldest" do
      buf = described_class.new
      id1 = buf.append("line A")       # 1 visual line
      id2 = buf.append("line B1\nline B2")  # 2 visual lines
      id3 = buf.append("line C")       # 1 visual line

      # Simulate 3 lines scrolled off the top
      committed = buf.commit_oldest_lines(3)

      expect(committed).to eq(2) # A and B fully scroll off
      expect(buf.entry_by_id(id1).committed).to be true
      expect(buf.entry_by_id(id2).committed).to be true
      expect(buf.entry_by_id(id3).committed).to be false
    end

    it "records a partial commit as a per-entry line offset, not by flipping committed" do
      # Partial scroll-off of a multi-line entry is tracked via
      # committed_line_offset so the still-visible suffix remains
      # repaintable while the already-scrolled prefix is hidden from
      # tail_lines / live_line_count. This is the fix for the
      # "multi-line entry leaks into a buffer repaint and shows up as
      # a duplicate of a line already in scrollback" bug.
      buf = described_class.new
      _id1 = buf.append("A")
      id2  = buf.append("B1\nB2\nB3")  # 3-line entry

      # Only 2 lines scroll off total — A (1 line) fully, then 1 line of B
      buf.commit_oldest_lines(2)

      # id2 is NOT fully committed (can't atomically finalize a split entry)
      expect(buf.entry_by_id(id2).committed).to be false
      # …but its first line IS recorded as committed via the offset.
      expect(buf.entry_by_id(id2).committed_line_offset).to eq(1)
      # Visible height reflects only the still-on-screen suffix.
      expect(buf.entry_by_id(id2).height).to eq(2)
      # And the committed prefix is hidden from tail_lines / live count.
      expect(buf.live_line_count).to eq(2)
      expect(buf.tail_lines(5)).to eq(["B2", "B3"])
    end

    it "is a no-op when nothing has scrolled" do
      buf = described_class.new
      id = buf.append("x")
      expect(buf.commit_oldest_lines(0)).to eq(0)
      expect(buf.entry_by_id(id).committed).to be false
    end
  end

  describe "#commit_through" do
    it "commits every entry up to and including the given id" do
      buf = described_class.new
      id1 = buf.append("a")
      id2 = buf.append("b")
      id3 = buf.append("c")
      buf.commit_through(id2)
      expect(buf.entry_by_id(id1).committed).to be true
      expect(buf.entry_by_id(id2).committed).to be true
      expect(buf.entry_by_id(id3).committed).to be false
    end
  end

  # ---------------------------------------------------------------------------
  # tail_lines / live_entries — what the renderer asks for
  # ---------------------------------------------------------------------------
  describe "#tail_lines" do
    it "returns the last N visual lines across live entries only" do
      buf = described_class.new
      buf.append("A")
      buf.append("B1\nB2")
      buf.append("C")
      expect(buf.tail_lines(3)).to eq(["B1", "B2", "C"])
      expect(buf.tail_lines(10)).to eq(["A", "B1", "B2", "C"])
    end

    it "skips committed entries — this is the double-render guard" do
      buf = described_class.new
      id1 = buf.append("already scrollback")
      buf.append("live 1")
      buf.append("live 2")
      buf.commit_through(id1)

      # Committed line must NOT appear here — it lives in terminal scrollback
      # already; if we returned it the renderer would paint it a second time.
      expect(buf.tail_lines(5)).to eq(["live 1", "live 2"])
    end

    it "returns [] for n <= 0" do
      buf = described_class.new
      buf.append("x")
      expect(buf.tail_lines(0)).to eq([])
    end
  end

  describe "#live_entries / #live_size / #live_line_count" do
    it "excludes committed entries" do
      buf = described_class.new
      id1 = buf.append("a")
      buf.append("b")
      buf.commit_through(id1)

      expect(buf.live_size).to eq(1)
      expect(buf.live_entries.map(&:id)).to eq([buf.live_entries.first.id])
      expect(buf.live_line_count).to eq(1)
    end
  end

  # ---------------------------------------------------------------------------
  # trim safety net
  # ---------------------------------------------------------------------------
  describe "max_entries cap" do
    it "drops oldest entries when the cap is exceeded" do
      buf = described_class.new(max_entries: 3)
      ids = 5.times.map { |i| buf.append("line #{i}") }
      expect(buf.size).to eq(3)
      # First two ids should be gone
      expect(buf.entry_by_id(ids[0])).to be_nil
      expect(buf.entry_by_id(ids[1])).to be_nil
      expect(buf.entry_by_id(ids[4])).not_to be_nil
    end
  end

  # ---------------------------------------------------------------------------
  # clear
  # ---------------------------------------------------------------------------
  describe "#clear" do
    it "drops everything including committed entries" do
      buf = described_class.new
      id = buf.append("x")
      buf.commit_through(id)
      buf.clear
      expect(buf.size).to eq(0)
      expect(buf.entry_by_id(id)).to be_nil
    end
  end
end

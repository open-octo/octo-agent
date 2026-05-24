# frozen_string_literal: true

RSpec.describe Clacky::Utils::LimitStack do
  # ---------------------------------------------------------------------------
  # Basic behaviour (existing semantics — must not regress)
  # ---------------------------------------------------------------------------
  describe "basic push / pop / size" do
    it "stores pushed items" do
      stack = described_class.new(max_size: 10)
      stack.push("a", "b", "c")
      expect(stack.to_a).to eq(["a", "b", "c"])
      expect(stack.size).to eq(3)
    end

    it "rolls off oldest items when max_size is exceeded" do
      stack = described_class.new(max_size: 3)
      stack.push("1", "2", "3", "4", "5")
      expect(stack.to_a).to eq(["3", "4", "5"])
      expect(stack.size).to eq(3)
    end

    it "marks truncated? true when lines roll off" do
      stack = described_class.new(max_size: 2)
      stack.push("a", "b", "c")
      expect(stack.truncated?).to be true
    end

    it "pop removes the last item and updates size" do
      stack = described_class.new(max_size: 10)
      stack.push("x", "y")
      stack.pop
      expect(stack.to_a).to eq(["x"])
    end

    it "clear resets all state" do
      stack = described_class.new(max_size: 10)
      stack.push("a", "b", "c")
      stack.clear
      expect(stack.empty?).to be true
      expect(stack.truncated?).to be false
    end

    it "push_lines splits text by newlines" do
      stack = described_class.new(max_size: 100)
      stack.push_lines("foo\nbar\nbaz\n")
      expect(stack.to_a).to eq(["foo\n", "bar\n", "baz\n"])
    end

    it "to_s joins all items" do
      stack = described_class.new(max_size: 10)
      stack.push("hello ", "world")
      expect(stack.to_s).to eq("hello world")
    end
  end

  # ---------------------------------------------------------------------------
  # max_line_chars
  # ---------------------------------------------------------------------------
  describe "max_line_chars" do
    it "truncates lines longer than max_line_chars" do
      stack = described_class.new(max_size: 100, max_line_chars: 10)
      stack.push("a" * 20)
      expect(stack.to_a.first.length).to eq(10)
    end

    it "preserves trailing newline after truncation" do
      stack = described_class.new(max_size: 100, max_line_chars: 10)
      stack.push("a" * 20 + "\n")
      stored = stack.to_a.first
      expect(stored.length).to eq(11)  # 10 chars + \n
      expect(stored).to end_with("\n")
    end

    it "marks truncated? true when a line is cut" do
      stack = described_class.new(max_size: 100, max_line_chars: 5)
      stack.push("hello world")
      expect(stack.truncated?).to be true
    end

    it "does not truncate lines at or below the limit" do
      stack = described_class.new(max_size: 100, max_line_chars: 10)
      stack.push("hello")
      expect(stack.truncated?).to be false
      expect(stack.to_a.first).to eq("hello")
    end

    it "nil max_line_chars leaves lines untouched" do
      stack = described_class.new(max_size: 100)
      long = "a" * 10_000
      stack.push(long)
      expect(stack.to_a.first).to eq(long)
    end
  end

  # ---------------------------------------------------------------------------
  # max_chars (total budget)
  # ---------------------------------------------------------------------------
  describe "max_chars" do
    it "stops accepting content once the budget is exhausted" do
      stack = described_class.new(max_size: 1000, max_chars: 20)
      stack.push_lines("a" * 10 + "\n")  # 11 chars (incl \n)
      stack.push_lines("b" * 10 + "\n")  # would exceed → partially accepted or dropped

      expect(stack.to_s.length).to be <= 20
    end

    it "marks truncated? true when budget is exceeded" do
      stack = described_class.new(max_size: 1000, max_chars: 5)
      stack.push("hello world")  # 11 chars > 5 → truncated
      expect(stack.truncated?).to be true
    end

    it "strictly keeps total chars within max_chars even with trailing newline" do
      # Regression: adding \n after cut must not push length over budget
      stack = described_class.new(max_size: 1000, max_chars: 10)
      stack.push_lines("a" * 20 + "\n")
      expect(stack.to_s.length).to be <= 10
    end

    it "drops subsequent pushes after budget is exhausted" do
      stack = described_class.new(max_size: 1000, max_chars: 10)
      stack.push("0123456789")   # exactly 10 chars → fills budget
      stack.push("extra")        # should be dropped entirely
      expect(stack.to_s).to eq("0123456789")
      expect(stack.truncated?).to be true
    end

    it "nil max_chars accepts unlimited content" do
      stack = described_class.new(max_size: 1000)
      stack.push("x" * 100_000)
      expect(stack.truncated?).to be false
    end
  end

  # ---------------------------------------------------------------------------
  # Interaction between limits
  # ---------------------------------------------------------------------------
  describe "combined limits" do
    it "max_size rolling window still works with max_chars" do
      stack = described_class.new(max_size: 3, max_chars: 1000)
      stack.push("a\n", "b\n", "c\n", "d\n")
      # max_size keeps last 3 lines
      expect(stack.to_a).to eq(["b\n", "c\n", "d\n"])
      expect(stack.truncated?).to be true
    end

    it "max_line_chars truncation counts towards total chars" do
      # max_line_chars: 5, max_chars: 10
      # Push "hello world\n" (12) → truncated to "hello\n" (6)
      # Push "hi\n" (3) → accepted, total now 9
      # Push "bye\n" (4) → would push total to 13, but remaining=1, so truncated
      stack = described_class.new(max_size: 100, max_line_chars: 5, max_chars: 10)
      stack.push_lines("hello world\n")  # stored as "hello\n" (6)
      stack.push_lines("hi\n")           # 3 chars, total=9
      stack.push_lines("bye\n")          # remaining=1, bye\n truncated/partially stored
      expect(stack.to_s.length).to be <= 10
      expect(stack.truncated?).to be true
    end
  end

  # ---------------------------------------------------------------------------
  # Backward-compat: existing callers pass only max_size
  # ---------------------------------------------------------------------------
  describe "backward compatibility" do
    it "works exactly as before when only max_size is given" do
      stack = described_class.new(max_size: 5)
      6.times { |i| stack.push("line#{i}\n") }
      expect(stack.size).to eq(5)
      expect(stack.to_a.first).to eq("line1\n")
      expect(stack.truncated?).to be true
    end
  end
end

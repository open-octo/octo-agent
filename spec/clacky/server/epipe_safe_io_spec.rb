# frozen_string_literal: true

require "spec_helper"
require "stringio"
require "clacky/server/epipe_safe_io"

RSpec.describe Clacky::Server::EPIPESafeIO do
  # A tiny IO double that raises Errno::EPIPE on the first write, then
  # behaves normally if reused (we don't expect to reuse it — the wrapper
  # should have swapped to /dev/null by then).
  class BrokenPipeIO
    attr_reader :write_attempts

    def initialize
      @write_attempts = 0
    end

    def write(*args)
      @write_attempts += 1
      raise Errno::EPIPE
    end

    def puts(*args)
      @write_attempts += 1
      raise Errno::EPIPE
    end

    def print(*args)
      @write_attempts += 1
      raise Errno::EPIPE
    end

    def flush
      raise Errno::EPIPE
    end
  end

  describe "healthy underlying IO" do
    let(:buffer) { StringIO.new }
    subject(:safe) { described_class.new(buffer) }

    it "delegates write to the underlying IO" do
      safe.write("hello")
      expect(buffer.string).to eq("hello")
    end

    it "delegates puts to the underlying IO" do
      safe.puts("hello")
      expect(buffer.string).to eq("hello\n")
    end

    it "delegates print to the underlying IO" do
      safe.print("a", "b")
      expect(buffer.string).to eq("ab")
    end

    it "delegates printf to the underlying IO" do
      safe.printf("%d-%s", 42, "x")
      expect(buffer.string).to eq("42-x")
    end

    it "delegates << to the underlying IO" do
      safe << "chained" << "!"
      expect(buffer.string).to eq("chained!")
    end

    it "delegates flush to the underlying IO" do
      expect { safe.flush }.not_to raise_error
    end

    it "reports fell_back? as false in healthy state" do
      safe.puts("ok")
      expect(safe.fell_back?).to be false
    end
  end

  describe "broken underlying IO" do
    let(:broken) { BrokenPipeIO.new }
    subject(:safe) { described_class.new(broken) }

    it "does not raise on first write" do
      expect { safe.write("oops") }.not_to raise_error
    end

    it "does not raise on puts" do
      expect { safe.puts("oops") }.not_to raise_error
    end

    it "does not raise on print" do
      expect { safe.print("oops") }.not_to raise_error
    end

    it "does not raise on flush" do
      expect { safe.flush }.not_to raise_error
    end

    it "marks itself as fell_back? after a failure" do
      safe.write("oops")
      expect(safe.fell_back?).to be true
    end

    it "subsequent writes go to /dev/null and do not re-hit broken IO" do
      safe.write("first")
      attempts_before = broken.write_attempts
      10.times { safe.puts("more") }
      # After fallback the wrapper no longer touches the broken IO.
      expect(broken.write_attempts).to eq(attempts_before)
    end

    it "remains usable after fallback (no exception storm)" do
      expect {
        100.times { |i| safe.puts("line #{i}") }
      }.not_to raise_error
      expect(safe.fell_back?).to be true
    end
  end

  describe "IOError handling" do
    # IOError is raised e.g. when writing to a closed IO.
    let(:closed_io) do
      io = StringIO.new
      io.close
      io
    end
    subject(:safe) { described_class.new(closed_io) }

    it "treats IOError on a closed IO the same as EPIPE" do
      expect { safe.puts("x") }.not_to raise_error
      expect(safe.fell_back?).to be true
    end
  end

  describe "drop-in for $stdout/$stderr" do
    it "can be assigned to $stdout and used by Kernel#puts transparently" do
      buf = StringIO.new
      original = $stdout
      begin
        $stdout = described_class.new(buf)
        puts "via Kernel"
      ensure
        $stdout = original
      end
      expect(buf.string).to eq("via Kernel\n")
    end

    it "Kernel#print, Kernel#printf, Kernel#p all flow through the wrapper" do
      buf = StringIO.new
      original = $stdout
      begin
        $stdout = described_class.new(buf)
        print "a"
        printf "%d", 42
        p "z"
      ensure
        $stdout = original
      end
      # print → "a", printf → "42", p → "\"z\"\n"
      expect(buf.string).to eq("a42\"z\"\n")
    end

    it "Kernel#warn flows through $stderr wrapper" do
      buf = StringIO.new
      original = $stderr
      begin
        $stderr = described_class.new(buf)
        warn "to stderr"
      ensure
        $stderr = original
      end
      expect(buf.string).to eq("to stderr\n")
    end

    it "Kernel#puts does not crash when $stdout is broken" do
      original = $stdout
      begin
        $stdout = described_class.new(BrokenPipeIO.new)
        expect { puts "should not crash" }.not_to raise_error
      ensure
        $stdout = original
      end
    end

    it "Kernel#warn does not crash when $stderr is broken" do
      original = $stderr
      begin
        $stderr = described_class.new(BrokenPipeIO.new)
        expect { warn "should not crash" }.not_to raise_error
      ensure
        $stderr = original
      end
    end
  end
end

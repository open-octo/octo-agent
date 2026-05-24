# frozen_string_literal: true

require "tmpdir"
require "fileutils"

RSpec.describe "SkillManager memory helpers" do
  # Minimal class that mixes in SkillManager and redirects memories_base_dir to tmpdir
  let(:agent_class) do
    Class.new do
      include Clacky::Agent::SkillManager

      attr_writer :memories_dir

      def memories_base_dir
        @memories_dir
      end

      public :load_memories_meta, :parse_memory_frontmatter
    end
  end

  let(:tmpdir) { Dir.mktmpdir }
  let(:agent)  { agent_class.new.tap { |a| a.memories_dir = tmpdir } }

  after { FileUtils.rm_rf(tmpdir) }

  # Helper: write a memory file and optionally back-date its mtime
  def write_memory(name, content, mtime: nil)
    path = File.join(tmpdir, name)
    File.write(path, content)
    FileUtils.touch(path, mtime: mtime) if mtime
    path
  end

  # ─────────────────────────────────────────────
  # parse_memory_frontmatter
  # ─────────────────────────────────────────────
  describe "#parse_memory_frontmatter" do
    it "parses YAML frontmatter correctly" do
      path = write_memory("test.md", <<~MD)
        ---
        topic: Ruby Tips
        description: Useful Ruby patterns
        updated_at: "2026-03-08"
        ---
        Some content here.
      MD

      fm = agent.parse_memory_frontmatter(path)
      expect(fm["topic"]).to eq("Ruby Tips")
      expect(fm["description"]).to eq("Useful Ruby patterns")
      expect(fm["updated_at"]).to eq("2026-03-08")
    end

    it "returns empty hash when no frontmatter present" do
      path = write_memory("plain.md", "Just plain content.\n")
      expect(agent.parse_memory_frontmatter(path)).to eq({})
    end

    it "returns empty hash on malformed YAML" do
      path = write_memory("bad.md", "---\n: bad: yaml:\n---\ncontent\n")
      expect(agent.parse_memory_frontmatter(path)).to eq({})
    end
  end

  # ─────────────────────────────────────────────
  # load_memories_meta — basic behavior
  # ─────────────────────────────────────────────
  describe "#load_memories_meta" do
    it "returns no-memories message when directory is empty" do
      expect(agent.load_memories_meta).to eq("(No long-term memories found.)")
    end

    it "returns no-memories message when directory does not exist" do
      agent.memories_dir = "/nonexistent/path/clacky/memories"
      expect(agent.load_memories_meta).to eq("(No long-term memories found.)")
    end

    it "lists memory files with topic, description and last-seen mtime" do
      write_memory("user.md", <<~MD)
        ---
        topic: User Profile
        description: Background info about the user
        ---
        Content.
      MD

      result = agent.load_memories_meta
      expect(result).to include("user.md")
      expect(result).to include("User Profile")
      expect(result).to include("Background info about the user")
      # mtime-based — always shows today's date for a freshly written file.
      expect(result).to include("last seen: #{Time.now.strftime("%Y-%m-%d")}")
    end

    it "ignores any legacy `updated_at` frontmatter field" do
      # Old memory files may still carry updated_at; we no longer surface it.
      write_memory("legacy.md", <<~MD)
        ---
        topic: Legacy
        description: Has stale frontmatter date
        updated_at: "2020-01-01"
        ---
        Content.
      MD

      result = agent.load_memories_meta
      expect(result).not_to include("2020-01-01")
      expect(result).not_to include("updated:")
      expect(result).to include("last seen:")
    end

    it "falls back to filename stem as topic when frontmatter is missing" do
      write_memory("my-topic.md", "No frontmatter here.\n")
      expect(agent.load_memories_meta).to include("my-topic")
    end

    it "shows '(no description)' when description is absent" do
      write_memory("nodesc.md", <<~MD)
        ---
        topic: Some Topic
        ---
        Content.
      MD
      expect(agent.load_memories_meta).to include("(no description)")
    end
  end

  # ─────────────────────────────────────────────
  # LRU: mtime ordering and Top-20 cap
  # ─────────────────────────────────────────────
  describe "#load_memories_meta — LRU ordering" do
    it "returns files sorted by mtime descending (most recently touched first)" do
      old_path = write_memory("old.md", "---\ntopic: Old\n---\n", mtime: Time.now - 3600)
      new_path = write_memory("new.md", "---\ntopic: New\n---\n", mtime: Time.now)

      result = agent.load_memories_meta
      expect(result.index("new.md")).to be < result.index("old.md")
    end

    it "promotes a file to the top after it is touched" do
      write_memory("alpha.md", "---\ntopic: Alpha\n---\n", mtime: Time.now - 7200)
      write_memory("beta.md",  "---\ntopic: Beta\n---\n",  mtime: Time.now - 3600)

      # alpha starts second; touch it to make it newest
      FileUtils.touch(File.join(tmpdir, "alpha.md"))

      result = agent.load_memories_meta
      expect(result.index("alpha.md")).to be < result.index("beta.md")
    end

    it "returns at most 20 files even when more exist" do
      25.times do |i|
        write_memory("mem-#{i.to_s.rjust(2, "0")}.md", "---\ntopic: Topic #{i}\n---\n",
                     mtime: Time.now - (25 - i) * 60)
      end

      result = agent.load_memories_meta
      # Count "| topic:" occurrences as a proxy for number of entries
      entry_count = result.scan("| topic:").length
      expect(entry_count).to eq(20)
    end

    it "surfaces the 20 most recently touched files when more than 20 exist" do
      25.times do |i|
        write_memory("mem-#{i.to_s.rjust(2, "0")}.md", "---\ntopic: Topic #{i}\n---\n",
                     mtime: Time.now - (25 - i) * 60)
      end

      result = agent.load_memories_meta
      # mem-24 is newest, mem-05 is the 20th newest; mem-04 and older should be absent
      expect(result).to include("mem-24.md")
      expect(result).to include("mem-05.md")
      expect(result).not_to include("mem-04.md")
    end
  end
end

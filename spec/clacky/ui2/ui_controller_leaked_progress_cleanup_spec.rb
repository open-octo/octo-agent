# frozen_string_literal: true

require "clacky/ui2/ui_controller"
require "clacky/ui2/progress_handle"

# Regression test for Layer-2 defense: even if a caller opens a legacy
# progress slot (show_progress(progress_type: X, phase: "active")) and
# forgets to close it with phase: "done", set_idle_status — which is
# invoked by the CLI after every agent turn — MUST still clean it up.
#
# This prevents the class of bugs where a retrying/idle_compress/etc.
# quiet handle's ticker thread keeps running after the task has
# completed, producing a frozen "Network failed ... (681s)" line.
RSpec.describe Clacky::UI2::UIController, "#set_idle_status closes leaked legacy progress handles" do
  # A minimal FakeOwner (like the one in progress_handle_spec) so we
  # can build a real ProgressHandle without a real UIController.
  class LeakedProgressFakeOwner
    attr_reader :events

    def initialize
      @events = []
      @stack  = []
      @mutex  = Mutex.new
      @next_id = 100
    end

    def register_progress(handle)
      @mutex.synchronize do
        @stack.push(handle)
        @events << [:register, handle.object_id]
        id = @next_id
        @next_id += 1
        id
      end
    end

    def unregister_progress(handle, final_frame:)
      @mutex.synchronize do
        @events << [:unregister, handle.object_id, final_frame]
        @stack.delete(handle)
      end
    end

    def render_frame(_handle, _frame); end
  end

  # Build a UIController skeleton without running its real initialize
  # (which would try to talk to a real terminal). We only need the
  # pieces that close_leaked_legacy_progress_handles touches.
  def build_skeleton_controller
    ctrl = Clacky::UI2::UIController.allocate
    ctrl.instance_variable_set(:@legacy_progress_handles, {})
    ctrl
  end

  it "finishes every legacy ProgressHandle that is still running" do
    ctrl = build_skeleton_controller
    owner = LeakedProgressFakeOwner.new

    # Simulate LlmCaller's retry path opening a quiet "retrying" slot.
    retrying = Clacky::UI2::ProgressHandle.new(
      owner: owner,
      message: "Network failed: Net::OpenTimeout (1/10)",
      style: :quiet
    )
    retrying.start

    # Simulate idle-compression opening its own quiet slot too.
    idle = Clacky::UI2::ProgressHandle.new(
      owner: owner,
      message: "Compressing conversation...",
      style: :quiet
    )
    idle.start

    ctrl.instance_variable_get(:@legacy_progress_handles).merge!(
      "retrying"      => retrying,
      "idle_compress" => idle
    )

    expect(retrying).to be_running
    expect(idle).to be_running

    ctrl.send(:close_leaked_legacy_progress_handles)

    expect(retrying).not_to be_running,
      "BUG: set_idle_status did not close the leaked 'retrying' handle — " \
      "its ticker will keep ticking after the task completes"
    expect(idle).not_to be_running,
      "BUG: set_idle_status did not close the leaked 'idle_compress' handle"

    # And the map is empty so a subsequent stale "active" for the same
    # type creates a fresh handle rather than being coalesced into a
    # long-dead one.
    expect(ctrl.instance_variable_get(:@legacy_progress_handles)).to be_empty
  end

  it "is a no-op when there are no legacy handles registered" do
    ctrl = build_skeleton_controller
    expect { ctrl.send(:close_leaked_legacy_progress_handles) }.not_to raise_error
  end

  it "is a no-op when the map is nil (pre-initialization path)" do
    ctrl = Clacky::UI2::UIController.allocate
    # @legacy_progress_handles not set at all
    expect { ctrl.send(:close_leaked_legacy_progress_handles) }.not_to raise_error
  end

  it "ignores handles that have already been finished (no double-finish)" do
    ctrl = build_skeleton_controller
    owner = LeakedProgressFakeOwner.new

    already_done = Clacky::UI2::ProgressHandle.new(owner: owner, message: "x", style: :quiet)
    already_done.start
    already_done.finish

    ctrl.instance_variable_get(:@legacy_progress_handles)["something"] = already_done

    expect(already_done).not_to be_running
    expect { ctrl.send(:close_leaked_legacy_progress_handles) }.not_to raise_error

    # Only one unregister event (the original finish), no second call.
    unregister_count = owner.events.count { |e| e[0] == :unregister }
    expect(unregister_count).to eq(1)
  end
end

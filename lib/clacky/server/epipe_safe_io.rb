# frozen_string_literal: true

require "delegate"

module Clacky
  module Server
    # EPIPESafeIO — wraps an IO ($stdout / $stderr) so that writes never raise
    # Errno::EPIPE / IOError to the calling code.
    #
    # Why this exists:
    #   The server worker process inherits fd 0/1/2 from the Master. If the
    #   Master itself was launched in a way where its stdout/stderr eventually
    #   becomes a broken pipe (e.g. launched by an installer that exits, or by
    #   a GUI/IDE process that closes its end), the worker's first `puts` after
    #   that pipe breaks raises Errno::EPIPE. Unhandled, this kills the worker
    #   — taking all in-memory sessions, agent loops, and SSE connections down
    #   with it, and triggering a crash loop because the new worker inherits
    #   the same broken fd.
    #
    # Behavior:
    #   - Healthy state: delegates every method to the underlying IO. Users
    #     see normal terminal output (banner, request logs, etc.).
    #   - First broken-pipe failure: catches Errno::EPIPE / IOError, swaps
    #     the underlying IO to /dev/null permanently, and returns silently.
    #     Subsequent writes succeed (into /dev/null) so the worker stays alive.
    #   - Session state, agent loops, SSE connections all preserved.
    #
    # Scope:
    #   We only wrap $stdout / $stderr (the global variables that Kernel#puts,
    #   Kernel#print, Kernel#warn, etc. use under the hood). We do NOT touch
    #   the STDOUT / STDERR constants — a codebase audit confirmed nothing in
    #   Clacky writes to those constants directly (only `STDOUT.flush` which
    #   cannot raise EPIPE).
    class EPIPESafeIO < SimpleDelegator
      # Methods that perform writes and may raise Errno::EPIPE.
      # We override each one to rescue and degrade gracefully.
      WRITE_METHODS = %i[write write_nonblock syswrite puts print printf << putc].freeze

      WRITE_METHODS.each do |m|
        define_method(m) do |*args, **kwargs, &blk|
          if kwargs.empty?
            __getobj__.public_send(m, *args, &blk)
          else
            __getobj__.public_send(m, *args, **kwargs, &blk)
          end
        rescue Errno::EPIPE, IOError => e
          fall_back_to_null!(e)
          # Retry the write into /dev/null so semantics (return value type) stay
          # close to what the caller expects. If even this fails, swallow it —
          # we must not raise from inside a write to $stdout/$stderr.
          begin
            if kwargs.empty?
              __getobj__.public_send(m, *args, &blk)
            else
              __getobj__.public_send(m, *args, **kwargs, &blk)
            end
          rescue StandardError
            nil
          end
        end
      end

      # Some callers do `$stdout.flush`. Make it safe too.
      def flush
        __getobj__.flush
      rescue Errno::EPIPE, IOError => e
        fall_back_to_null!(e)
        nil
      end

      # Whether this wrapper has already fallen back to /dev/null.
      # Useful for tests and diagnostics.
      def fell_back?
        @fell_back == true
      end

      private def fall_back_to_null!(error)
        return if @fell_back

        @fell_back = true
        begin
          # Best-effort: try to log once via Clacky::Logger if available.
          # Wrapped in rescue because Logger itself might be mid-init.
          if defined?(Clacky::Logger)
            Clacky::Logger.warn(
              "[EPIPESafeIO] Underlying IO broken (#{error.class}: #{error.message}); " \
              "falling back to /dev/null. Worker stays alive."
            )
          end
        rescue StandardError
          # ignore
        end

        begin
          null = File.open(File::NULL, "w")
          null.sync = true
          __setobj__(null)
        rescue StandardError
          # If even opening /dev/null fails, leave the original object — at
          # worst the next write raises again and we rescue again.
        end
      end
    end
  end
end

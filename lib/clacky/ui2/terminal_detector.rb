# frozen_string_literal: true

module Clacky
  module UI2
    # TerminalDetector - Detect terminal background color before UI starts
    class TerminalDetector
      # Detect if terminal has dark background
      # Uses multiple strategies to determine background color
      # @return [Boolean] true if dark background, false if light background
      def self.detect_dark_background
        # Strategy 1: Check $COLORFGBG environment variable (fast, set by some terminals)
        if ENV.key?('COLORFGBG')
          # Format is like "15;0" where second number is background ANSI code
          # 0-7 are dark, 8-15 are light
          parts = ENV['COLORFGBG'].split(';')
          if parts.size >= 2
            bg_code = parts.last.to_i
            if bg_code >= 0 && bg_code <= 15
              return bg_code < 8
            end
          end
        end
        
        # Strategy 2: Query terminal background using OSC 11 sequence
        begin
          rgb = query_terminal_background_color
          if rgb
            # Calculate luma (perceived brightness): 0.0 (black) to 1.0 (white)
            # Formula: 0.299*R + 0.587*G + 0.114*B
            luma = (0.299 * rgb[:r] + 0.587 * rgb[:g] + 0.114 * rgb[:b]) / 255.0
            return luma < 0.5
          end
        rescue => e
          # Silently fall through to default
        end
        
        # Default: assume dark background (most common for terminals)
        true
      end

      # Query terminal background color using OSC 11 sequence
      # This should be called BEFORE UI starts to avoid interference
      # @return [Hash, nil] RGB hash like {r: 26, g: 43, b: 60} or nil if failed
      def self.query_terminal_background_color
        require 'io/console'
        
        # Only works on TTY
        return nil unless $stdin.tty?
        return nil unless $stdout.tty?
        
        old_state = nil
        begin
          # Save current terminal state
          old_state = $stdin.raw!
          
          # Clear any pending input first
          while IO.select([$stdin], nil, nil, 0)
            $stdin.read_nonblock(1000) rescue break
          end
          
          # Send OSC 11 query: ESC ] 11 ; ? ST
          # Use ST terminator (ESC \) instead of BEL for better compatibility
          $stdout.print "\e]11;?\e\\\\"
          $stdout.flush
          
          # Read response with timeout (terminal should respond quickly)
          response = String.new  # Use String.new to create mutable string
          timeout = 0.1  # 100ms timeout
          start_time = Time.now
          
          while Time.now - start_time < timeout
            if IO.select([$stdin], nil, nil, 0.01)
              char = $stdin.read_nonblock(1) rescue nil
              break unless char
              response << char
              
              # Look for complete response pattern
              # Response format: ESC ] 11 ; rgb:RRRR/GGGG/BBBB BEL or ST
              if response.match?(/\e\]11;rgb:[0-9a-fA-F]+\/[0-9a-fA-F]+\/[0-9a-fA-F]+(\e\\|\a)/)
                break
              end
              
              # Safety: stop if response gets too long (probably garbage)
              break if response.length > 100
            end
          end
          
          # Parse response: look for rgb:RRRR/GGGG/BBBB
          # Example: ]11;rgb:1a2b/3c4d/5e6f or ]11;rgb:ffff/ffff/ffff
          if response =~ /rgb:([0-9a-fA-F]+)\/([0-9a-fA-F]+)\/([0-9a-fA-F]+)/
            r_hex, g_hex, b_hex = $1, $2, $3
            # Take first 2 hex digits (terminals may return 4 or 2 hex digits per channel)
            r = r_hex[0, 2].to_i(16)
            g = g_hex[0, 2].to_i(16)
            b = b_hex[0, 2].to_i(16)
            return { r: r, g: g, b: b }
          end
          
          nil
        rescue => e
          # If anything goes wrong, return nil to fall back to default
          nil
        ensure
          # Make sure we restore terminal state even if error occurs
          old_state.restore if old_state rescue nil
          
          # Clear any remaining input to prevent leakage
          begin
            while IO.select([$stdin], nil, nil, 0)
              $stdin.read_nonblock(1000) rescue break
            end
          rescue
            # Ignore cleanup errors
          end
        end
      end
    end
  end
end

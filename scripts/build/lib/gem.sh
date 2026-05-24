# gem.sh — gem source configuration helpers
# Depends-On: colors.sh
# Requires-Vars: $SHELL_RC
# Sets-Vars: $GEM_HOME $GEM_PATH
# Include via: @include lib/gem.sh
#
# configure_gem_source  — point ~/.gemrc at CN mirror (or restore default)
#                         Requires: $USE_CN_MIRRORS $CN_RUBYGEMS_URL (from network.sh)
# restore_gemrc         — undo configure_gem_source (used by uninstaller)
# setup_gem_home        — redirect GEM_HOME to user dir when system gem dir
#                         is not writable; writes env vars to $SHELL_RC
# restore_gem_home      — remove GEM_HOME block from $SHELL_RC (uninstaller)
# --------------------------------------------------------------------------

configure_gem_source() {
    local gemrc="$HOME/.gemrc"

    if [ "$USE_CN_MIRRORS" = true ]; then
        if grep -q "${CN_RUBYGEMS_URL}" "$gemrc" 2>/dev/null; then
            print_success "gem source already → ${CN_RUBYGEMS_URL}"
        else
            [ -f "$gemrc" ] && mv "$gemrc" "$HOME/.gemrc_clackybak"
            cat > "$gemrc" <<GEMRC
:sources:
  - ${CN_RUBYGEMS_URL}
GEMRC
            print_success "gem source → ${CN_RUBYGEMS_URL}"
        fi
    else
        if [ -f "$gemrc" ] && grep -q "${CN_RUBYGEMS_URL}" "$gemrc" 2>/dev/null; then
            if [ -f "$HOME/.gemrc_clackybak" ]; then
                mv "$HOME/.gemrc_clackybak" "$gemrc"
                print_info "gem source restored from backup"
            else
                rm "$gemrc"
                print_info "gem source restored to default"
            fi
        fi
    fi
}

restore_gemrc() {
    local gemrc="$HOME/.gemrc"
    local gemrc_bak="$HOME/.gemrc_clackybak"
    if [ -f "$gemrc_bak" ]; then
        mv "$gemrc_bak" "$gemrc"
        print_success "~/.gemrc restored from backup"
    elif [ -f "$gemrc" ]; then
        rm "$gemrc"
        print_success "~/.gemrc removed"
    else
        print_info "~/.gemrc — nothing to restore"
    fi
}

# --------------------------------------------------------------------------
# Redirect GEM_HOME to user dir when system Ruby gem dir is not writable
# --------------------------------------------------------------------------
setup_gem_home() {
    local gem_dir
    gem_dir=$(gem environment gemdir 2>/dev/null || true)
    [ -w "$gem_dir" ] && return 0

    local ruby_api
    ruby_api=$(ruby -e 'puts RbConfig::CONFIG["ruby_version"]' 2>/dev/null)
    export GEM_HOME="$HOME/.gem/ruby/${ruby_api}"
    export GEM_PATH="$HOME/.gem/ruby/${ruby_api}"
    export PATH="$HOME/.gem/ruby/${ruby_api}/bin:$PATH"
    print_info "System Ruby detected — gems will install to ~/.gem/ruby/${ruby_api}"

    if [ -n "$SHELL_RC" ] && ! grep -q "GEM_HOME" "$SHELL_RC" 2>/dev/null; then
        {
            echo ""
            echo "# Ruby user gem dir (added by openclacky installer)"
            echo "export GEM_HOME=\"\$HOME/.gem/ruby/${ruby_api}\""
            echo "export GEM_PATH=\"\$HOME/.gem/ruby/${ruby_api}\""
            echo "export PATH=\"\$HOME/.gem/ruby/${ruby_api}/bin:\$PATH\""
        } >> "$SHELL_RC"
        print_info "GEM_HOME written to $SHELL_RC"
    fi
}

restore_gem_home() {
    [ -n "$SHELL_RC" ] && [ -f "$SHELL_RC" ] || return 0
    grep -q "GEM_HOME" "$SHELL_RC" 2>/dev/null || return 0
    # Remove the block written by setup_gem_home (comment + 3 export lines)
    local tmp
    tmp=$(mktemp)
    grep -v "# Ruby user gem dir (added by openclacky installer)" "$SHELL_RC" \
        | grep -v "export GEM_HOME=" \
        | grep -v "export GEM_PATH=" \
        | grep -v "/.gem/ruby/" \
        > "$tmp" && mv "$tmp" "$SHELL_RC"
    print_success "GEM_HOME removed from $SHELL_RC"
}

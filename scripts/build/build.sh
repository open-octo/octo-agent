#!/bin/bash
# build.sh — compile .sh.cc templates into standalone shell scripts
#
# Usage:
#   bash scripts/build/build.sh              # build all, output to scripts/
#   bash scripts/build/build.sh install.sh   # build one specific output file
#   bash scripts/build/build.sh --diff       # build to tmp/, diff against git HEAD
#
# Template syntax (inside .sh.cc files):
#   @include lib/foo.sh    — inline the contents of scripts/build/lib/foo.sh
#                            (strips comment header lines starting with "# ")

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR="$SCRIPT_DIR/src"
LIB_DIR="$SCRIPT_DIR"                        # @include paths relative to build/
OUT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"      # scripts/
TMP_DIR="$OUT_DIR/tmp"                       # scripts/tmp/ (diff staging area)

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

info()    { echo -e "${BLUE}ℹ${NC}  $1"; }
success() { echo -e "${GREEN}✓${NC}  $1"; }
warn()    { echo -e "${YELLOW}⚠${NC}  $1"; }
error()   { echo -e "${RED}✗${NC}  $1"; }
section() { echo -e "\n${CYAN}▶${NC}  $1"; }

# --------------------------------------------------------------------------
# Dependency checker — validates @include order in .sh.cc files
#
# Each lib declares in its header:
#   # Depends-On: colors.sh os.sh        (requires these libs to be included first)
#   # Requires-Vars: $FOO $BAR           (requires these vars set by prior libs)
#   # Sets-Vars: $FOO $BAR               (vars this lib sets, for downstream checks)
#
# check_include_deps <lib_path> <included_libs_so_far...>
#   Called each time an @include is processed. Exits 1 if deps are violated.
# --------------------------------------------------------------------------

# Parse "# Key: val1 val2 ..." from a lib header, print space-separated values
_parse_header_field() {
    local lib_file="$1"
    local field="$2"   # e.g. "Depends-On" or "Requires-Vars" or "Sets-Vars"
    local raw
    raw=$(grep -m1 "^# ${field}:" "$lib_file" 2>/dev/null | sed "s/^# ${field}:[[:space:]]*//" | tr -d '\r')
    # "(none)" means empty
    [ "$raw" = "(none)" ] && echo "" || echo "$raw"
}

# Build a set of all vars set by the listed libs
_vars_set_by_libs() {
    local lib_dir="$1"; shift
    local libs=("$@")
    for lib in "${libs[@]}"; do
        # libs are stored as "lib/foo.sh" — resolve relative to LIB_DIR parent
        local lib_file="$lib_dir/$lib"
        [ -f "$lib_file" ] || continue
        _parse_header_field "$lib_file" "Sets-Vars"
    done
}

check_include_deps() {
    local lib_path="$1"; shift   # e.g. "lib/network.sh"
    local included=("$@")        # libs included so far (e.g. "lib/colors.sh" "lib/os.sh")

    local lib_file="$LIB_DIR/$lib_path"
    [ -f "$lib_file" ] || return 0   # missing file caught by resolve_include

    local lib_name; lib_name=$(basename "$lib_path")
    local ok=true

    # --- Check Depends-On ---
    local depends_on
    depends_on=$(_parse_header_field "$lib_file" "Depends-On")
    for dep in $depends_on; do
        local dep_path="lib/$dep"
        local found=false
        for inc in "${included[@]}"; do
            [ "$inc" = "$dep_path" ] && found=true && break
        done
        if [ "$found" = false ]; then
            error "[$lib_name] Depends-On '$dep' but it was not @included before this line"
            ok=false
        fi
    done

    # --- Check Requires-Vars ---
    local requires_vars
    requires_vars=$(_parse_header_field "$lib_file" "Requires-Vars")

    # Collect all vars set by already-included libs
    local sets_raw
    sets_raw=$(_vars_set_by_libs "$LIB_DIR" "${included[@]}")
    local sets_list
    sets_list=$(echo "$sets_raw" | tr ' ' '\n' | grep '^\$' | sort -u)

    for var in $requires_vars; do
        [ "$var" = "(none)" ] && continue
        if ! echo "$sets_list" | grep -qxF "$var"; then
            error "[$lib_name] Requires-Vars '$var' but no prior @include Sets it"
            ok=false
        fi
    done

    [ "$ok" = true ] || exit 1
}


resolve_include() {
    local lib_file="$LIB_DIR/$1"

    if [ ! -f "$lib_file" ]; then
        error "Include not found: $1  (looked in $LIB_DIR)"
        exit 1
    fi

    local past_header=false
    while IFS= read -r line || [ -n "$line" ]; do
        if [ "$past_header" = false ]; then
            local is_comment=""
            [[ "$line" =~ ^#[[:space:]] ]] && is_comment=1 || true
            [ "$line" = "#" ] && is_comment=1 || true
            if [ -n "$is_comment" ]; then
                continue
            else
                past_header=true
            fi
        fi
        echo "$line"
    done < "$lib_file"
}

# --------------------------------------------------------------------------
# render_template <src_file> <out_file>
# --------------------------------------------------------------------------
render_template() {
    local src="$1"
    local out="$2"
    local tmp_file="${out}.tmp"
    local included_libs=()   # track @include order for dep checking

    : > "$tmp_file"

    while IFS= read -r line || [ -n "$line" ]; do
        local include_path=""
        [[ "$line" =~ ^@include[[:space:]]+(.+)$ ]] && include_path="${BASH_REMATCH[1]}" || true

        if [ -n "$include_path" ]; then
            check_include_deps "$include_path" "${included_libs[@]}"
            included_libs+=("$include_path")
            echo ""                                       >> "$tmp_file"
            echo "# ---[ @include ${include_path} ]---"  >> "$tmp_file"
            resolve_include "$include_path"               >> "$tmp_file"
            echo ""                                       >> "$tmp_file"
        else
            echo "$line" >> "$tmp_file"
        fi
    done < "$src"

    mv "$tmp_file" "$out"
    chmod +x "$out"
}

# --------------------------------------------------------------------------
# build_one <src_cc_file> <output_dir>
# --------------------------------------------------------------------------
build_one() {
    local src="$1"
    local out_dir="$2"
    local name out
    name=$(basename "$src" .cc)   # e.g. install.sh
    out="$out_dir/$name"

    info "Building $name ..."
    render_template "$src" "$out"
    success "→ $out"
}

# --------------------------------------------------------------------------
# cmd_build — normal build, output to scripts/
# --------------------------------------------------------------------------
cmd_build() {
    local targets=("$@")

    echo ""
    echo "╔═══════════════════════════════════════════════╗"
    echo "║   🔨  Shell Script Builder (.sh.cc → .sh)    ║"
    echo "╚═══════════════════════════════════════════════╝"
    echo ""

    if [ ${#targets[@]} -gt 0 ]; then
        for target in "${targets[@]}"; do
            local src="$SRC_DIR/${target}.cc"
            [ -f "$src" ] || { error "Source not found: $src"; exit 1; }
            build_one "$src" "$OUT_DIR"
        done
    else
        local found=0
        for src in "$SRC_DIR"/*.sh.cc; do
            [ -f "$src" ] || continue
            build_one "$src" "$OUT_DIR"
            found=$((found + 1))
        done
        [ "$found" -eq 0 ] && warn "No .sh.cc files found in $SRC_DIR"
    fi

    echo ""
    success "Build complete. Output → $OUT_DIR"
    echo ""
}

# --------------------------------------------------------------------------
# cmd_diff — build to tmp/, diff each file against git HEAD version
# --------------------------------------------------------------------------
cmd_diff() {
    echo ""
    echo "╔═══════════════════════════════════════════════════════════╗"
    echo "║   🔍  Diff: build output vs git HEAD (scripts/*.sh)      ║"
    echo "╚═══════════════════════════════════════════════════════════╝"
    echo ""

    # Require git
    command -v git >/dev/null 2>&1 || { error "git not found"; exit 1; }

    # Find repo root (scripts/ is one level above build/)
    local repo_root
    repo_root=$(cd "$OUT_DIR" && git rev-parse --show-toplevel 2>/dev/null) || {
        error "Not inside a git repository"
        exit 1
    }

    # Build into tmp/
    mkdir -p "$TMP_DIR"
    info "Building all templates into tmp/ ..."
    echo ""

    for src in "$SRC_DIR"/*.sh.cc; do
        [ -f "$src" ] || continue
        build_one "$src" "$TMP_DIR"
    done

    # Diff each generated file against git HEAD
    echo ""
    section "Comparing tmp/ output against git HEAD ..."
    echo ""

    local any_diff=false

    for new_file in "$TMP_DIR"/*.sh; do
        [ -f "$new_file" ] || continue
        local name; name=$(basename "$new_file")
        local rel_path="scripts/$name"

        # Get HEAD version from git
        local head_content
        head_content=$(cd "$repo_root" && git show "HEAD:${rel_path}" 2>/dev/null) || {
            warn "$name — not found in git HEAD (new file)"
            continue
        }

        # Write HEAD version to a temp file for diff
        local head_file="$TMP_DIR/${name}.HEAD"
        echo "$head_content" > "$head_file"

        # Run diff
        local diff_output
        diff_output=$(diff -u "$head_file" "$new_file" 2>/dev/null) || true

        if [ -z "$diff_output" ]; then
            success "$name — identical to HEAD"
        else
            any_diff=true
            echo -e "${YELLOW}⚠${NC}  ${YELLOW}$name — has differences:${NC}"
            echo ""
            # Colorize diff output: + lines green, - lines red, @@ cyan
            echo "$diff_output" | while IFS= read -r dline; do
                case "$dline" in
                    "+++"*|"---"*) echo -e "${BLUE}${dline}${NC}" ;;
                    "@@"*)         echo -e "${CYAN}${dline}${NC}"  ;;
                    "+"*)          echo -e "${GREEN}${dline}${NC}" ;;
                    "-"*)          echo -e "${RED}${dline}${NC}"   ;;
                    *)             echo "$dline"                   ;;
                esac
            done
            echo ""
        fi

        rm -f "$head_file"
    done

    # Clean up tmp/
    rm -rf "$TMP_DIR"

    echo ""
    if [ "$any_diff" = true ]; then
        warn "Some files differ from git HEAD — review the diff above."
    else
        success "All outputs are identical to git HEAD."
    fi
    echo ""
}

# --------------------------------------------------------------------------
# Main — dispatch on first argument
# --------------------------------------------------------------------------
main() {
    case "${1:-}" in
        --diff)
            cmd_diff
            ;;
        --help|-h)
            echo "Usage:"
            echo "  bash build.sh              # build all .sh.cc → scripts/"
            echo "  bash build.sh install.sh   # build one file"
            echo "  bash build.sh --diff       # build to tmp/, diff vs git HEAD"
            ;;
        *)
            cmd_build "$@"
            ;;
    esac
}

main "$@"

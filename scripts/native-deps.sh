#!/bin/sh
set -eu

. ./build/toolchain.lock

product=$1
root=$2
repo=$(pwd)
downloads="$repo/.deps/downloads"
mkdir -p "$root" "$downloads"
lock="$downloads/.flows-$product.lock"
if ! shlock -p "$$" -f "$lock"; then
    printf 'Another %s dependency build is running\n' "$product" >&2
    exit 1
fi
scratch=
trap 'if [ -n "$scratch" ]; then rm -rf "$scratch"; fi; rm -f "$lock"' EXIT
trap 'exit 1' HUP INT TERM
scratch=$(mktemp -d "$root/.stage-$product.XXXXXX")

verify() {
    printf '%s  %s\n' "$1" "$2" | shasum -a 256 -c -
}

fetch() {
    url=$1
    archive=$2
    checksum=$3
    if [ ! -f "$archive" ]; then
        curl --fail --silent --show-error --location --retry 2 --output "$scratch/download" "$url"
        verify "$checksum" "$scratch/download"
        mv "$scratch/download" "$archive"
    fi
    verify "$checksum" "$archive"
}

publish() {
    if [ ! -d "$root/$product" ]; then
        mv "$scratch/output" "$root/$product"
    fi
}

verify_fonts() {
    for style in Regular Italic Bold BoldItalic; do
        test -s "$1/Iosevka-$style.ttf"
    done
    test -s "$1/OFL.txt"
    swift -module-cache-path "$repo/.deps/swift-cache" "$repo/scripts/font-family.swift" "$1/"*.ttf
}

verify_library() {
    lipo "$1" -verify_arch "$MACOS_ARCH"
    nm -gU "$1" > "$scratch/symbols"
    grep -q "[[:space:]]_$2$" "$scratch/symbols"
}

verify_ghostty() {
    test -s "$1/include/ghostty.h"
    test -s "$1/include/ghostty/vt.h"
    test -s "$1/LICENSE"
    verify_library "$1/lib/libghostty-surface.a" ghostty_surface_new
    verify_library "$1/lib/libghostty-vt.dylib" ghostty_terminal_new
}

case "$product" in
    fonts)
        name="PkgTTF-Iosevka-$IOSEVKA_VERSION.zip"
        archive="$downloads/$name"
        fetch "https://github.com/be5invis/Iosevka/releases/download/v$IOSEVKA_VERSION/$name" "$archive" "$IOSEVKA_SHA256"
        if [ ! -d "$root/fonts" ]; then
            mkdir "$scratch/output"
            for style in Regular Italic Bold BoldItalic; do
                unzip -p "$archive" "Iosevka-$style.ttf" > "$scratch/output/Iosevka-$style.ttf"
            done
            cp desktop/assets/fonts/OFL.txt "$scratch/output/OFL.txt"
            verify_fonts "$scratch/output"
            publish
        else
            verify_fonts "$root/fonts"
        fi
        ;;
    ghostty)
        test "$(uname -s)" = Darwin
        test "$(uname -m)" = "$MACOS_ARCH"
        sdk=$(xcrun --sdk macosx --show-sdk-version)
        if [ "$sdk" != "$MACOS_SDK_VERSION" ]; then
            printf 'macOS SDK %s required, found %s\n' "$MACOS_SDK_VERSION" "$sdk" >&2
            exit 1
        fi
        src_name="ghostty-$GHOSTTY_VERSION.tar.gz"
        src_archive="$downloads/$src_name"
        zig_name="zig-aarch64-macos-$ZIG_VERSION.tar.xz"
        zig_archive="$downloads/$zig_name"
        fetch "https://release.files.ghostty.org/$GHOSTTY_VERSION/$src_name" "$src_archive" "$GHOSTTY_SHA256"
        fetch "https://ziglang.org/download/$ZIG_VERSION/$zig_name" "$zig_archive" "$ZIG_SHA256"
        if [ ! -d "$root/ghostty" ]; then
            mkdir "$scratch/source" "$scratch/compiler"
            tar -xf "$src_archive" -C "$scratch/source" --strip-components 1
            tar -xf "$zig_archive" -C "$scratch/compiler" --strip-components 1
            test "$("$scratch/compiler/zig" version)" = "$ZIG_VERSION"
            export ZIG_GLOBAL_CACHE_DIR="$repo/.deps/zig-cache"
            (
                cd "$scratch/source"
                "$scratch/compiler/zig" build -Doptimize=ReleaseFast \
                    -Dversion-string="$GHOSTTY_VERSION" \
                    -Dapp-runtime=none -Demit-xcframework=true -Dxcframework-target=native \
                    -Demit-macos-app=false --prefix "$scratch/output"
            )
            set -- "$scratch/source/macos/GhosttyKit.xcframework/macos-$MACOS_ARCH/"*.a
            test "$#" -eq 1
            cp "$1" "$scratch/output/lib/libghostty-surface.a"
            cp "$scratch/source/include/ghostty.h" "$scratch/output/include/"
            cp "$scratch/source/LICENSE" "$scratch/output/"
            # The upstream pkg-config file embeds the temporary install prefix.
            if [ -f "$scratch/output/share/pkgconfig/libghostty-vt.pc" ]; then
                sed "s|$scratch/output|$root/ghostty|g" "$scratch/output/share/pkgconfig/libghostty-vt.pc" > "$scratch/libghostty-vt.pc"
                mv "$scratch/libghostty-vt.pc" "$scratch/output/share/pkgconfig/libghostty-vt.pc"
            fi
            verify_ghostty "$scratch/output"
            publish
        else
            verify_ghostty "$root/ghostty"
        fi
        ;;
    *)
        printf 'Unknown native dependency: %s\n' "$product" >&2
        exit 2
        ;;
esac

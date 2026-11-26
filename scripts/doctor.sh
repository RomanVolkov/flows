#!/bin/sh
set -eu

. ./build/toolchain.lock

missing=0
probe() {
    category=$1
    tool=$2
    shift 2
    if command -v "$tool" >/dev/null 2>&1; then
        if version=$("$tool" "$@" 2>&1); then
            printf '%s %s: %s\n' "$category" "$tool" "$version"
            return
        fi
        printf '%s %s: version probe failed (%s)\n' "$category" "$tool" "$version"
    else
        printf '%s %s: missing\n' "$category" "$tool"
    fi
    if [ "$category" != optional ]; then
        missing=1
    fi
}

printf 'Target: macOS %s, %s, SDK %s\n' "$MACOS_DEPLOYMENT_TARGET" "$MACOS_ARCH" "$MACOS_SDK_VERSION"
probe required "$1" version
probe required git --version
probe native xcodebuild -version
probe native xcrun --sdk macosx --show-sdk-version
probe native xcrun --find metal
probe native clang --version
probe native swift --version
for tool in curl tar unzip shasum lipo nm shlock; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'native %s: missing\n' "$tool"
        missing=1
    fi
done
printf 'native Zig: %s (downloaded and verified by make libghostty)\n' "$ZIG_VERSION"
if [ -x "$2" ]; then
    probe lint "$2" -version
else
    printf 'lint staticcheck: missing (make lint installs %s)\n' "$STATICCHECK_VERSION"
fi
for provider in claude codex agy gh glab; do
    probe optional "$provider" --version
done
exit "$missing"

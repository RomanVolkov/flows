#!/bin/sh
set -eu

VERSION="${VERSION:-dev}"
ARCH="${ARCH:-$(uname -m)}"
OUT_DIR="${OUT_DIR:-build/dist}"
APP_DIR="${OUT_DIR}/Flows.app"
CONTENTS="${APP_DIR}/Contents"

rm -rf "${APP_DIR}"
mkdir -p "${CONTENTS}/MacOS" \
  "${CONTENTS}/Resources/bin" \
  "${CONTENTS}/Resources/plugins" \
  "${CONTENTS}/Resources/workflows" \
  "${CONTENTS}/Resources/spec/v1" \
  "${CONTENTS}/Resources/licenses"

cp build/macos/Info.plist "${CONTENTS}/Info.plist"
if [ -f build/macos/AppIcon.icns ]; then
  cp build/macos/AppIcon.icns "${CONTENTS}/Resources/AppIcon.icns"
fi
if command -v plutil >/dev/null 2>&1; then
  plutil -replace CFBundleShortVersionString -string "${VERSION}" "${CONTENTS}/Info.plist"
fi

# Build core binaries (CLI in Resources/bin/flows so it never collides with MacOS/Flows on case-insensitive APFS).
go build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=${VERSION}" \
  -o "${CONTENTS}/Resources/bin/flows" "./cmd/flows"
for name in flowsd flows-host; do
  go build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=${VERSION}" \
    -o "${CONTENTS}/MacOS/${name}" "./cmd/${name}"
done
clang -fobjc-arc -framework AppKit -framework CoreText desktop/macos/main.m -o "${CONTENTS}/MacOS/Flows"

# Prebuild shipped plugins so the packaged app runs without a compiler.
for plugin in git github gitlab jira youtrack zulip; do
  mkdir -p "${CONTENTS}/Resources/plugins/${plugin}"
  cp "plugins/${plugin}/tool.yaml" "${CONTENTS}/Resources/plugins/${plugin}/tool.yaml"
  go build -trimpath -o "${CONTENTS}/Resources/plugins/${plugin}/${plugin}-plugin" "./plugins/${plugin}/cmd/plugin"
done

# Bundle built-in workflows and spec/v1.
cp -R workflows/* "${CONTENTS}/Resources/workflows/"
cp -R spec/v1/* "${CONTENTS}/Resources/spec/v1/"
cp schema/workflow-v1.json "${CONTENTS}/Resources/spec/v1/workflow.schema.json"
cp schema/tool-v1.json "${CONTENTS}/Resources/spec/v1/tool.schema.json"

# Sign with configured Developer ID or ad-hoc for local development.
if command -v codesign >/dev/null 2>&1; then
  if [ -n "${FLOWS_CODESIGN_IDENTITY:-}" ]; then
    codesign --force --deep --sign "${FLOWS_CODESIGN_IDENTITY}" "${APP_DIR}"
  else
    codesign --force --deep --sign - "${APP_DIR}" >/dev/null 2>&1 || true
  fi
fi

ARCHIVE="${OUT_DIR}/Flows-${VERSION}-darwin-${ARCH}.tar.gz"
tar -C "${OUT_DIR}" -czf "${ARCHIVE}" "Flows.app"
shasum -a 256 "${ARCHIVE}" > "${ARCHIVE}.sha256"
echo "packaged ${ARCHIVE}"

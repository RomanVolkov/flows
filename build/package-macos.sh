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
  "${CONTENTS}/Resources/fonts" \
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
make package-daemon
cp build/bin/flowsd-release "${CONTENTS}/MacOS/flowsd"
for name in flows-host; do
  go build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=${VERSION}" \
    -o "${CONTENTS}/MacOS/${name}" "./cmd/${name}"
done
make desktop
cp desktop/app/frontend/src/assets/fonts/iosevka-term/LICENSE.md "${CONTENTS}/Resources/licenses/IosevkaTermNerdFontMono.md"
cp desktop/app/frontend/src/assets/fonts/iosevka-term/*.ttf "${CONTENTS}/Resources/fonts/"
cp build/bin/Flows-desktop "${CONTENTS}/MacOS/Flows"

# Bundle built-in workflows, spec/v1, and Go SDK for offline tool compilation.
cp -R workflows/* "${CONTENTS}/Resources/workflows/"
cp -R spec/v1/* "${CONTENTS}/Resources/spec/v1/"
cp schema/workflow-v1.json "${CONTENTS}/Resources/spec/v1/workflow.schema.json"
cp schema/tool-v1.json "${CONTENTS}/Resources/spec/v1/tool.schema.json"
mkdir -p "${CONTENTS}/Resources/spec/v1/sdk/go"
cp -R sdk/go/* "${CONTENTS}/Resources/spec/v1/sdk/go/"

# Distribute shipped plugins as Go source code by default so Flow compiles them using the host Go compiler.
# Set FLOWS_PREBUILD_PLUGINS=1 to also include prebuilt plugin binaries alongside sources.
for plugin in git github gitlab jira youtrack zulip; do
  mkdir -p "${CONTENTS}/Resources/plugins/${plugin}/cmd/plugin"
  cp "plugins/${plugin}/tool.yaml" "${CONTENTS}/Resources/plugins/${plugin}/tool.yaml"
  cp plugins/${plugin}/cmd/plugin/*.go "${CONTENTS}/Resources/plugins/${plugin}/cmd/plugin/"
  cat > "${CONTENTS}/Resources/plugins/${plugin}/go.mod" <<EOF
module flows-plugin-${plugin}

go 1.26.10

require github.com/romanvolkov/flows/sdk/go v0.0.0

replace github.com/romanvolkov/flows/sdk/go => ../../spec/v1/sdk/go
EOF
  if [ "${FLOWS_PREBUILD_PLUGINS:-0}" = "1" ]; then
    go build -trimpath -o "${CONTENTS}/Resources/plugins/${plugin}/${plugin}-plugin" "./plugins/${plugin}/cmd/plugin"
  fi
done

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

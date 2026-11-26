#!/bin/sh
set -eu

VERSION="${VERSION:-dev}"
ARCH="${ARCH:-$(uname -m)}"
OUT_DIR="${OUT_DIR:-build/dist}"
APP_DIR="${OUT_DIR}/Flows.app"
CONTENTS="${APP_DIR}/Contents"

rm -rf "${APP_DIR}"
mkdir -p "${CONTENTS}/MacOS" \
  "${CONTENTS}/Resources/plugins" \
  "${CONTENTS}/Resources/workflows" \
  "${CONTENTS}/Resources/spec/v1" \
  "${CONTENTS}/Resources/licenses"

cp build/macos/Info.plist "${CONTENTS}/Info.plist"
if command -v plutil >/dev/null 2>&1; then
  plutil -replace CFBundleShortVersionString -string "${VERSION}" "${CONTENTS}/Info.plist"
fi

# Build core binaries.
for name in flows flowsd flows-host; do
  go build -trimpath -ldflags "-X github.com/romanvolkov/flows/internal/command.Version=${VERSION}" \
    -o "${CONTENTS}/MacOS/${name}" "./cmd/${name}"
done
if [ ! -e "${CONTENTS}/MacOS/Flows" ]; then
  cp "${CONTENTS}/MacOS/flows" "${CONTENTS}/MacOS/Flows"
fi

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
  SIGN_ID="${FLOWS_CODESIGN_IDENTITY:--}"
  codesign --force --deep --sign "${SIGN_ID}" "${APP_DIR}" >/dev/null 2>&1 || true
fi

ARCHIVE="${OUT_DIR}/Flows-${VERSION}-darwin-${ARCH}.tar.gz"
tar -C "${OUT_DIR}" -czf "${ARCHIVE}" "Flows.app"
shasum -a 256 "${ARCHIVE}" > "${ARCHIVE}.sha256"
echo "packaged ${ARCHIVE}"

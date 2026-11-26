#!/bin/sh
set -eu

VERSION="${VERSION:-0.1.0-test}"
ARCH="${ARCH:-$(uname -m)}"
OUT_DIR="$(mktemp -d)"
trap 'rm -rf "${OUT_DIR}"' EXIT

VERSION="${VERSION}" ARCH="${ARCH}" OUT_DIR="${OUT_DIR}" sh build/package-macos.sh

APP="${OUT_DIR}/Flows.app/Contents"
for bin in Flows flows flowsd flows-host; do
  test -x "${APP}/MacOS/${bin}"
done
for plugin in git github gitlab jira youtrack zulip; do
  test -f "${APP}/Resources/plugins/${plugin}/tool.yaml"
  test -x "${APP}/Resources/plugins/${plugin}/${plugin}-plugin"
done
test -f "${APP}/Info.plist"
test -f "${APP}/Resources/spec/v1/authoring.md"
test -f "${APP}/Resources/workflows/review-open-prs/workflow.yaml"
test -f "${APP}/Resources/workflows/deliver-change/workflow.yaml"

ARCHIVE="${OUT_DIR}/Flows-${VERSION}-darwin-${ARCH}.tar.gz"
test -f "${ARCHIVE}"
test -f "${ARCHIVE}.sha256"
(cd "${OUT_DIR}" && shasum -a 256 -c "$(basename "${ARCHIVE}.sha256")")

echo "release_test passed"

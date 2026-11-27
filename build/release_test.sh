#!/bin/sh
set -eu

VERSION="${VERSION:-0.1.0-test}"
ARCH="${ARCH:-$(uname -m)}"
OUT_DIR="$(mktemp -d)"
trap 'rm -rf "${OUT_DIR}"' EXIT

VERSION="${VERSION}" ARCH="${ARCH}" OUT_DIR="${OUT_DIR}" sh build/package-macos.sh

APP="${OUT_DIR}/Flows.app/Contents"
for bin in Flows flowsd flows-host; do
  test -x "${APP}/MacOS/${bin}"
done
test -x "${APP}/Resources/bin/flows"
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

# Execute packaged CLI binary, lint Info.plist, and verify plugin handshake.
"${APP}/Resources/bin/flows" version | grep -q "${VERSION}"
if command -v plutil >/dev/null 2>&1; then
  plutil -lint "${APP}/Info.plist" >/dev/null
fi
printf '{"protocol":1,"kind":"describe","request_id":"d1"}\n' | "${APP}/Resources/plugins/git/git-plugin" | grep -q '"git.checkout"'

# Exercise the same library initialization as both installers in an isolated home.
export FLOWS_HOME="${OUT_DIR}/test-home"
cli="${APP}/Resources/bin/flows"
"$cli" install-bundled
for plugin in git github gitlab jira youtrack zulip; do
  test -x "${FLOWS_HOME}/config/tools/${plugin}/${plugin}-plugin"
done
for wf in review-open-prs deliver-change; do
  "$cli" validate "${FLOWS_HOME}/config/workflows/${wf}"
done
"$cli" catalog describe git.checkout --json | grep -q '"enabled":true'

mv "${FLOWS_HOME}/config/workflows/review-open-prs" "${OUT_DIR}/removed-workflow"
mv "${FLOWS_HOME}/config/tools/git" "${OUT_DIR}/removed-tool"
prompt="${FLOWS_HOME}/config/workflows/deliver-change/prompts/group.md"
echo 'User edit' >> "$prompt"
"$cli" install-bundled
test ! -e "${FLOWS_HOME}/config/workflows/review-open-prs"
test ! -e "${FLOWS_HOME}/config/tools/git"
grep -q 'User edit' "$prompt"
if "$cli" catalog describe git.checkout --json >/dev/null 2>&1; then
  echo 'Removed starter tool still in catalog' >&2
  exit 1
fi

echo "release_test passed"

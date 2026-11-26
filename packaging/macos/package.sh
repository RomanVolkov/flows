#!/bin/sh
set -eu
: "${VERSION:?set VERSION to a release version}"
dist=${OUT_DIR:-build/dist}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/root/Applications"
ditto "$dist/Flows.app" "$stage/root/Applications/Flows.app"
# Keep updates at the declared path even when another copy is present.
pkgbuild --analyze --root "$stage/root" "$stage/components.plist"
/usr/libexec/PlistBuddy -c 'Set :0:BundleIsRelocatable false' "$stage/components.plist"
pkgbuild --root "$stage/root" --component-plist "$stage/components.plist" --install-location / \
  --ownership preserve --identifier dev.flows.desktop --version "$VERSION" \
  --scripts packaging/macos/scripts "$stage/component.pkg"
pkg="$dist/Flows-$VERSION-darwin-arm64.pkg"
productbuild --distribution packaging/macos/distribution.xml --package-path "$stage" "$pkg"
(cd "$dist" && shasum -a 256 "Flows-$VERSION-darwin-arm64.pkg" > "Flows-$VERSION-darwin-arm64.pkg.sha256")

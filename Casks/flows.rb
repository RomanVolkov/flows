cask "flows" do
  version "1.1.1"
  sha256 "63f94493c6cbc9a24831c6c6085b32b06a6a30809dd95e77ed65abd1e03401ad"

  url "https://github.com/RomanVolkov/flows/releases/download/v#{version}/Flows-#{version}-darwin-arm64.pkg"
  name "Flows"
  desc "Canvas workspace for AI harnesses, tools, and workflows"
  homepage "https://github.com/RomanVolkov/flows"

  depends_on arch: :arm64
  depends_on macos: ">= :sonoma"
  depends_on formula: "go"
  container type: :naked

  installer script: {
    executable: "/usr/sbin/installer",
    args:       ["-pkg", "#{staged_path}/Flows-#{version}-darwin-arm64.pkg", "-target", "CurrentUserHomeDirectory"],
    sudo:       false,
  }
  binary "#{Dir.home}/Applications/Flows.app/Contents/Resources/bin/flows"

  uninstall launchctl: "dev.flows.daemon",
            quit:      "dev.flows.app",
            delete:    [
              "~/.local/bin/flows",
              "~/Applications/Flows.app",
              "~/Library/LaunchAgents/dev.flows.daemon.plist",
            ]
end

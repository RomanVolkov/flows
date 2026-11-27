cask "flows" do
  version "1.0.0"
  sha256 "e31990eaadb3adf485d9a17c1ec02fe36a404ffebf18e191f0d306dac6509c6a"

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

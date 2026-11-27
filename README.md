# Flows

[![Latest release](https://img.shields.io/github/v/release/RomanVolkov/flows)](https://github.com/RomanVolkov/flows/releases/latest)

**Run AI coding sessions and repeatable engineering workflows from one macOS workspace.**

Flows gives you a canvas for working with Claude Code, Codex, and Antigravity CLI. Open a terminal
for a quick coding session, or build a workflow that calls tools, hands work to agents, and pauses
for your approval. The canvas shows what ran, what is waiting, and where you can step in.

For example, a review workflow can list open pull requests through GitHub, start an agent to
review each diff, and notify you when the reviews finish. You can inspect each step and open the
agent's terminal during the run.

## What you can do

- **Work with coding agents.** Start Claude Code, Codex, or Antigravity CLI (`agy`) in a local repository
  and open their terminals from the canvas.
- **Run repeatable workflows.** Combine commands, reusable tools, and agent prompts. Start work as
  prerequisites finish, run a step for each item in a list, or wait for a condition.
- **Decide when a workflow can proceed.** Add a question or approval before the next step. Inspect
  failures and interrupted runs on the canvas.
- **Create your own workflows and tools.** Describe the idea in a guided conversation. Review which
  tools to reuse, which to build, and what to assign to agents before approving implementation.
  Import existing packages and see validation issues in Settings.
- **See token usage.** Compare recorded usage by harness and model in Analytics.

## Get Flows

Flows runs on Apple Silicon Macs with macOS 14 or later.

```sh
brew tap romanvolkov/flows https://github.com/RomanVolkov/flows.git
brew install --cask romanvolkov/flows/flows
```

You can also download the Apple Silicon installer from the [latest GitHub release](https://github.com/RomanVolkov/flows/releases/latest).
Install Go first if you use the installer directly. Flows uses it to build the bundled tools from source.
If you installed through Homebrew, update with `brew upgrade --cask romanvolkov/flows/flows`.

## Start your first session or workflow

1. Install and sign in to your preferred coding agent. Open **Flows** from Applications and check
   that the agent appears in **Settings**.
2. Press `⌘N`. Choose **Session · coding harness** to open a terminal in a local repository, or
   choose **Run a workflow** to pick a starter workflow.
3. Select a node on the canvas to inspect its progress, open its terminal, or answer a question.

### Workflows included with Flows

| Workflow | What it does |
| --- | --- |
| **Review open PRs** | Finds open GitHub pull requests or GitLab merge requests and runs an agent review for each. Set up `gh` or `glab` for the provider you use. |
| **Deliver change** | Takes a change request, proposes task groups for approval, and has agents draft and review plans. |
| **Validate package** | Checks a folder containing a workflow or tool manifest for configuration errors and missing dependencies. |

To add a package, choose **New → Import a workflow or external tool** and select its folder.
Once Flows prepares a workflow package, it appears in **Run a workflow**.

## Learn more

- [Write, import, and validate workflows](docs/workflows.md)
- [Build a tool for Flows](docs/plugins.md)
- [Install from source or troubleshoot](docs/operations.md)
- [Report a problem or suggest an improvement](https://github.com/RomanVolkov/flows/issues)

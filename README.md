# baifo

A personal AI agent that lives entirely in your terminal. You talk to one agent;
it orchestrates a crew of sub-agents to do the heavy lifting. A single binary, no
daemon to babysit, no web UI, no external services to wire up.

## What it is

baifo is a terminal-first, single-user, local assistant. You launch it, land in a
conversation with the root agent, and it decides on the fly when to spawn workers,
load skills, search its memory, run shell commands, or call tools. There is no
flow editor and no DAG to draw: the orchestration is the model's job, not yours.

It speaks the A2A (Agent-to-Agent) protocol, so other systems can drive your root
agent the same way you do.

## Features

- **Live sub-agent orchestration.** The root spawns workers on demand, either from
  templates you define or composed at runtime (prompt, model, tools, skills and
  secret access chosen per spawn). Watch any worker run in real time, or jump into
  its chat and talk to it directly, without leaving the terminal.

- **Long-term memory that works offline.** baifo remembers facts across sessions
  and retrieves them by meaning, not just keywords. It needs no vector database, no
  embedding API, and no network access: the memory works out of the box with the
  binary you downloaded.

- **Secrets the model never sees.** Store credentials once; agents reference them by
  name. baifo injects the real value only at the moment a tool runs and scrubs it
  back out of the results before the model (or the logs, or the audit trail) ever
  sees it. Encrypted at rest, or plaintext for a throwaway box.

- **Tools, built in and pluggable.** Ships with filesystem access (read, write,
  edit, search, run commands) and web fetch/search. Connect external MCP tool
  servers over HTTP or stdio, with OAuth handled for you.

- **Skills.** Drop a Markdown playbook into your config to give agents domain
  knowledge (a coding style, a research protocol, a checklist) without bloating
  every prompt. Each agent opts into the ones it needs.

- **Tunable thinking.** Dial each agent's reasoning effort from minimal to high.
  baifo maps it to whatever the underlying model supports and leaves it alone when
  the model has no such knob.

- **Pick your model per agent.** Mix providers freely: a cheap fast model for
  routine workers, a heavyweight for the hard parts. baifo knows each provider's
  catalogue so it can size a worker sensibly.

- **Run it your way.** Full interactive TUI, a headless REPL, one-shot commands from
  your shell, or a background server that exposes your agent over A2A.

## Providers

baifo works with OpenAI, Anthropic, Gemini, any OpenAI-compatible endpoint, and
Ollama. Configure one or more and point each agent at whichever you want.

## Install

Grab the latest pre-built binary for your OS and architecture from the
[Releases page](https://github.com/achetronic/baifo/releases) and drop it on your
PATH.

Or build from source (Go 1.25+):

```bash
git clone https://github.com/achetronic/baifo.git
cd baifo
make build
sudo mv bin/baifo /usr/local/bin/
```

## Getting started

```bash
baifo
```

On first run baifo walks you through creating its config directory. From there,
everything inside the TUI is driven by slash commands (`/agent`, `/mcp`, `/skill`,
`/provider`, `/secret`, `/fact`, `/session`, `/worker`, `/settings`), each with
autocomplete and inline help.

## Commands

```
baifo                          launch the interactive TUI (default)
baifo --version                print version
baifo --config-dir <path>      point at a specific config directory
baifo chat [--message <text>]  headless REPL, or a one-shot message with --message
baifo server                   run the A2A server in the background
baifo secrets set <NAME>       store a secret (interactive, masked)
baifo secrets list             list names and descriptions, never values
baifo secrets rotate <NAME>    rotate a secret
baifo secrets unset <NAME>     remove a secret
```

## Configuration

baifo keeps everything in one directory: providers, agents, secrets, skills and
runtime knobs. The first run creates it for you; edit it by hand or through the
in-TUI slash commands. The full schema reference lives in `.agents/CONFIG.md`.

## Contributing

Contributions are welcome.

1. Fork the project.
2. Create a feature branch (`git checkout -b feature/AmazingFeature`).
3. Commit your changes (`git commit -m 'Add AmazingFeature'`).
4. Push the branch (`git push origin feature/AmazingFeature`).
5. Open a Pull Request.

Before pushing, run `make build`, `make test` and `make lint`.

## License

Apache-2.0. See [LICENSE](LICENSE).

<div align="center">
  Made with ❤️ from the Canary Islands 🇮🇨
</div>

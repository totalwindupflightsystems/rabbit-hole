# Support

## Getting Help

Rabbit-Hole is an open-source project. Support is community-driven.

- **Bug reports & feature requests:** open an issue on the [GitLab repository](https://gitlab.readydedis.com/rabbit-hole/rabbit-hole/-/issues)
- **Documentation:** see `docs/` and the `specs/` directory for architecture, API, and protocol details
- **Discussions:** use the repository's issue tracker for questions about usage, deployment, or extension

## Scope

We provide best-effort support for:

- Building from source (`make build`)
- Running the CLI and server (`rabbit-hole serve`)
- Understanding the collect → classify → express architecture
- Extending the classifier backend interface (local Gemma or remote gRPC)

We do **not** provide:

- 24/7 SLA-backed support
- Support for modified forks (check the source first)

## Before Asking

1. Read the README and relevant spec (`specs/01-Overview-Architecture.md` onward).
2. Search existing issues for your problem.
3. Include your Go version (`go version`), OS/kernel, and the command you ran.
4. If attaching an eBPF trace, note whether you are running as root (required for attachment).

## Security Issues

Do **not** file public issues for security vulnerabilities. Contact the maintainers privately via the GitLab repository's security channel.

# Governance

Rabbit-Hole is a legibility layer between agents and humans — an open-source
project for capturing, classifying, and querying what autonomous agents do
inside your infrastructure. This document defines how the project is governed.

## Project Structure

- **Repository:** [gitlab.readydedis.com/rabbit-hole/rabbit-hole](https://gitlab.readydedis.com/rabbit-hole/rabbit-hole)
- **License:** See [LICENSE](LICENSE)
- **Specs:** `specs/` (S01–S07, axiom-level: architecture, collection,
  classification, expression, storage, CLI, dashboard)

## Roles

### Maintainers

Maintainers own the project's direction and the quality of its codebase. Their
responsibilities:

- Review and merge changes to `main`
- Keep the specification set (`specs/`) current with the implementation
- Triage issues and security reports
- Manage releases and version tags
- Enforce the Contributor Covenant (see [CODE_OF_CONDUCT](CODE_OF_CONDUCT.md))

### Contributors

Anyone may open issues and merge requests. Contributions must:

- Follow the architecture in the specs — new layers or protocol changes
  require a spec update first
- Pass the repository's quality gates (build, vet, tests, lint, secret scan)
- Include tests for new behavior; the project treats unit + integration
  coverage as a requirement, not a nicety

### Automated Foreman

The project runs a coding-hermes foreman that executes the task board in
`.coding-hermes/board/tasks.jsonl`. The foreman is a contributor, not a maintainer: it
spawns workers, verifies quality gates, and commits against board tasks, but
release decisions and spec-level architecture changes remain with maintainers.

## Decision Making

- **Small, well-scoped changes** (bug fixes, tests, docs): merge after review
  by one maintainer and a green pipeline.
- **Feature or architectural changes**: require a spec entry (new S0x file or
  an amendment to an existing spec) and discussion with the maintainers before
  implementation. The task board is spec-driven; no spec, no phase.
- **Releases**: see [Release Process](#release-process) below.
- **Disagreements**: escalate to the lead maintainer, whose decision is final.
  Record material decisions in `docs/` so the rationale survives.

## Code Review

- Every merge request to `main` must pass: `go build ./...`, `go vet ./...`,
  `go test ./... -count=1 -short`, `gofmt` cleanliness, and the secret scan.
- Reviewers check for: spec compliance, test coverage of new code paths,
  kernel-telemetry safety (eBPF code must not regress collector stability),
  and backward compatibility of the storage format.
- Review comments are binding; unresolved threads block the merge.

## Release Process

1. Maintainers agree on a version and update `CHANGELOG.md`.
2. Tag and release on GitLab (`/releases`) with prebuilt binaries:
   `dist/rabbit-hole-linux-amd64`, `dist/rabbit-hole-linux-arm64`.
3. Each release ships checksums (SHA-256) so users can verify downloads.
4. The self-hosted CLI guide (`specs/06-CLI-Self-Hosted.md`) documents the
   zero-to-working setup path and is verified against each release.

## Contact

- Issues: repository issue tracker
- Security: [SECURITY.md](SECURITY.md) (report privately before disclosing)
- Discussions: open a thread in the repository issue tracker

Community standards are defined in [CODE_OF_CONDUCT](CODE_OF_CONDUCT.md) and
[SUPPORT](SUPPORT.md) describes how to get help.

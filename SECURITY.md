# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| 1.0.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in Rabbit-Hole, please report it privately via:

- Email: security@totalwindupflightsystems.com

Please do NOT open a public issue for security vulnerabilities.

### What to include

- Description of the vulnerability
- Steps to reproduce
- Affected versions
- Any potential mitigations you've identified

### Response timeline

- Acknowledgment: within 48 hours
- Assessment: within 5 business days
- Fix timeline: depends on severity, typically 7-30 days

## Security Model

Rabbit-Hole runs as a system daemon with eBPF privileges (CAP_BPF, CAP_SYS_ADMIN). 

### Key security considerations

- **eBPF privilege escalation**: The collector requires CAP_BPF and CAP_SYS_ADMIN. Run the daemon as a dedicated, unprivileged user with only the necessary capabilities.
- **gRPC transport**: When using remote classification, always use TLS (mTLS recommended).
- **API authentication**: The Express API supports API key authentication. Always enable it in production.
- **SQLite database**: The database contains trace data including LLM prompts and responses. Restrict file permissions.
- **Context windows**: Chat responses may expose sensitive data from agent traces. Authenticate all API access.

## Responsible Disclosure

We follow a coordinated disclosure process. We ask that you:

1. Give us reasonable time to investigate and fix the issue
2. Do not exploit the vulnerability beyond what is necessary to demonstrate it
3. Do not disclose the vulnerability publicly until we've released a fix

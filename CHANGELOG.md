# Changelog

## v0.9.0-beta (2025-11-12)

Initial beta release.

**Features:**
- Web-based wizard UI (6-step flow)
- VictoriaMetrics metrics export via /api/v1/export
- Data obfuscation (IP addresses with 777.777.x.x, job names)
- Multi-tenant support
- Cross-platform binaries (8 architectures)
- Multiple authentication methods (Basic, Bearer, Header, None)
- Automatic component discovery
- Real-time export progress

**Testing:**
- 50+ unit tests
- 31 E2E tests (Playwright)
- 14 Docker test scenarios

**Known Issues:**
- Limited production testing
- API may change in future versions
- Documentation is minimal

**Feedback:** Please report issues on GitHub.

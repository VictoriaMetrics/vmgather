# Security

## Beta Notice

VMExporter is currently in beta.

## Reporting Security Issues

Please report security concerns to: info@victoriametrics.com

**DO NOT** open public GitHub issues for security vulnerabilities.

## Best Practices

When using VMExporter:
- Always use HTTPS for VictoriaMetrics connections
- Use authentication (do not expose endpoints publicly)
- Review obfuscation settings before export
- Credentials are not stored by VMExporter
- Verify SHA256 checksums of downloaded binaries

## Known Security Considerations

- Credentials are transmitted but not persisted
- Obfuscated data is deterministic (same input = same output)

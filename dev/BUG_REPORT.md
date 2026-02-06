# VMGather Code Review Bug Report

Date: 2026-02-06

This document is an internal engineering review of the `/Users/yu-key/VMexporter` workspace (upstream: `VictoriaMetrics/vmgather`).

## Scope and repo state

- Repo: `VictoriaMetrics/vmgather` (local path: `/Users/yu-key/VMexporter`)
- Current branch: `feature/custom-mode-oneshot` (HEAD `5345454`)
- Baseline for comparison: `origin/main` (`42d8f0e`)
- Local-only commits on top of `origin/main` (not pushed):
  - `2ffad40` test: preserve `go test` exit codes
  - `5345454` ui/e2e: stabilize advanced sections and test server
- Working tree: clean (except this report file while editing)

## Validation executed locally

Baseline commands executed in this review cycle:

- `make test-full`: PASS (unit + integration; Docker env is left running)
- `go build -o vmgather ./cmd/vmgather`: PASS (used by Playwright `webServer` command)
- `cd tests/e2e && npx playwright test --workers=1`: PASS (99 passed / 3 skipped)
- `make test-race`: PASS (after fixes; previously exposed data races in `internal/server` and `internal/importer/server`)

## Full test baseline (current HEAD)

This is the “everything” run requested (unit + integration + E2E) to establish a baseline.

- `make test-full`: PASS (unit + integration)
- `make test-race`: PASS
- `cd tests/e2e && npx playwright test --workers=1`: PASS (99 passed / 3 skipped)

## Bugfix tracker (ordered)

Status legend: TODO -> IN PROGRESS -> DONE.

1. [P0][DONE] Fix data races caught by `make test-race` (ExportJobManager start/status snapshot; importer upload job snapshot)
2. [P0][DONE] Makefile test targets must fail on `go test` failure (exit code masking via pipe)
3. [P1][DONE] Help section auto-opens on time range input (breaks UX + E2E assumptions)
4. [P1][DONE] Obfuscation advanced `<details>` are open by default (click-to-open tests close them)
5. [P1][DONE] Sample loading error/spinner tests rely on stable open/close behavior for advanced sections
6. [P0][DONE] Release version injection is broken (ldflags cannot override `const`)
7. [P0][DONE] HTTP client timeout (30s) is incompatible with streaming exports and batch timeouts
8. [P0][TODO] Resumed export progress double-counts batches (progress/ETA can be wildly wrong)
9. [P1][TODO] `CheckExportAPI` leaks response bodies (connection leak on success path)
10. [P1][TODO] Regex injection / query correctness risk when building `{job=~"..."}`
11. [P1][TODO] Canceled jobs are never removed by retention cleanup
12. [P1][TODO] Hard-coded 15 minute job timeout can kill legitimate exports
13. [P2][TODO] `/api/fs/*` endpoints enlarge security surface (especially if bound to non-localhost)
14. [P2][TODO] Debug/diagnostic logging is noisy by default
15. [P2][TODO] Documentation inconsistencies (customer-facing confusion)

## Test suite expansion and hardening plan

Goal: make the test suite a reliable gate for iterative bug fixes (fast feedback, minimal flakiness).

1. Stabilize test environment readiness
   - Verified: `local-test-env/healthcheck.sh` already waits for `vmauth-export-test` `vm_app_version` (tenant 2022).
2. Make E2E selectors resilient
   - Replace brittle text-based preset selectors in Playwright with stable selectors (ideally a `data-testid` or `data-preset` attribute; fallback: updated text to match the UI).
3. Reduce E2E flakiness during local “full” runs
   - Prefer running full E2E with `--workers=1` (shared server state, filesystem, and export jobs are not isolated per test).
4. Add targeted regression tests per bug fix
   - For concurrency bugs: add unit tests that fail under `-race` and then validate via `make test-race`.
   - For behavior bugs: add unit-level tests where possible, and only add E2E assertions when the bug is UI-contract specific.
5. Ensure Makefile test targets preserve exit codes
   - Implemented (`2ffad40`): capture `go test` exit code and return it after formatting output (portable, no `pipefail` dependency).
   - Result: `make test-race` now reliably fails on real races (used to expose `ExportJobManager` and importer job snapshot races, now fixed).
6. Avoid stale `webServer` reuse in Playwright by default
   - Implemented: `tests/e2e/playwright.config.js` now reuses existing server only when `PW_REUSE_EXISTING_SERVER=1` is explicitly set.

## Findings (prioritized)

### P0: Release version injection is broken (ldflags cannot override `const`)

**Status**: DONE

**Impact**
- Release builds produced by `build/builder.go` likely report the wrong runtime version in logs and bundle metadata.
- This breaks support/debug flows because customers can’t reliably confirm what binary they ran.

**Evidence**
- `cmd/vmgather/main.go:24-26` declares `const version = "1.4.1"`.
- `cmd/vmimporter/main.go:20` declares `const version = "0.1.0"`.
- `build/builder.go:132-136` uses `-ldflags "-X main.version=..."` which only works for a `var`, not a `const`.
- Repro: building with `go build -ldflags "-X main.version=TESTVER" ./cmd/vmgather` still logs `vmgather v1.4.1 starting...` (observed locally).

**Suggested fix**
- Replace `const version = "..."` with `var version = "dev"` (or empty) in both `cmd/vmgather` and `cmd/vmimporter`.
- Ensure both binaries print `dev` when not set, and allow `-X main.version` to override.
- Add a small unit test per binary package that checks `version` is not a `const` (or that `version != hardcodedRelease`), plus a CI sanity check that `-ldflags -X` changes the startup banner.

**Implemented**
- `cmd/vmgather`: `version` is now a `var` with default `dev` (ldflags-overridable).
- `cmd/vmimporter`: `version` is now a `var` with default `dev` (ldflags-overridable).
- Added compile-time regression tests: `cmd/vmgather/version_test.go`, `cmd/vmimporter/version_test.go`.

---

### P0: HTTP client timeout (30s) is incompatible with streaming exports and batch timeouts

**Status**: DONE

**Impact**
- Long-running `/api/v1/export` responses may be cut off at 30s regardless of per-batch contexts.
- This can manifest as partial bundles, unexpected export failures, or retries that look like “random timeouts” to customers.

**Evidence**
- `internal/infrastructure/vm/client.go:76-82` sets `http.Client{Timeout: 30 * time.Second}`.
- Export batches use a longer deadline: `internal/application/services/export_service.go:23` (`defaultBatchTimeout = 2 * time.Minute`) and `internal/application/services/export_service.go:121-123` (`context.WithTimeout(..., defaultBatchTimeout)`).
- `http.Client.Timeout` is a hard cap for the entire request including reading the body, so it will override the 2m context and truncate slow exports.

**Suggested fix**
- Prefer request-scoped timeouts via `context.WithTimeout` and remove/zero `http.Client.Timeout` (or set it to a value >= max expected batch duration).
- If keeping a client-level timeout, make it configurable and ensure it is always >= the largest per-request timeout used by services.
- Add an integration test that simulates a slow streaming `/api/v1/export` response and verifies it is not cut off by the client.

**Implemented**
- Removed the global `http.Client.Timeout` from `internal/infrastructure/vm/client.go` so request-scoped contexts can control streaming runtime.
- Added a regression assertion in `internal/infrastructure/vm/client_test.go` that `NewClient` leaves `httpClient.Timeout` unset (0).

**Verification**
- `make test-full`: PASS
- `make test-race`: PASS
- `cd tests/e2e && npx playwright test --workers=1`: PASS

---

### P0: Resumed export progress double-counts batches (progress/ETA can be wildly wrong)

**Impact**
- After resume, the UI can show progress > 100%, incorrect ETA, and wrong “completed batches”.
- This undermines trust in the progress UI and makes resume behavior appear broken.

**Evidence**
- Resume sets the next start batch to the number of completed batches:
  - `internal/server/export_jobs.go:148-152` (`resumeFrom := job.status.CompletedBatches`, then `cfg.ResumeFromBatch = resumeFrom`).
- Export reports `BatchIndex` as the 1-based absolute index in the full run:
  - `internal/application/services/export_service.go:143-149` sets `BatchIndex: batchIndex + 1`.
- Job manager adds the “base” again:
  - `internal/server/export_jobs.go:264` sets `CompletedBatches = baseBatches + progress.BatchIndex`.
  - Example: resumeFrom=10, first resumed progress.BatchIndex=11 => CompletedBatches becomes 21.

**Suggested fix**
- Make `BatchProgress.BatchIndex` semantics explicit.
- Option A (minimal): treat `BatchIndex` as absolute and set `CompletedBatches = max(baseBatches, progress.BatchIndex)` (and ensure monotonicity).
- Option B: emit “relative batch index” after resume (but then `TotalBatches` and UI texts must be adjusted accordingly).
- Add a unit test for resume behavior covering:
  - initial run partial completion
  - resume
  - correct `CompletedBatches`, `Progress`, `ETA` monotonicity

---

### P0: Data races in export job manager (ExportJobManager)

**Status**: DONE

**Impact**
- `make test-race` fails and/or the process can crash under concurrency (`concurrent map read and map write`) if the job manager is exercised concurrently (start/cancel/resume/cleanup).

**Evidence**
- `internal/server/export_jobs.go` had two distinct race issues:
  - `StartJob` returned `status.clone()` after starting the async goroutine, while `markRunning()` could update the same `status` concurrently.
  - `runJob` read `m.jobs[jobID]` without holding `m.mu`, while other goroutines can mutate `m.jobs` under lock.
- Repro (before fix): `make test-race` failed (race detected during `internal/server.TestHandleExportCancel`).

**Fix**
- `StartJob`: snapshot the initial status under `m.mu` before starting `runJob`.
- `runJob`: read `m.jobs[jobID]` under `m.mu.RLock()`.

**Verification**
- `make test-race`: PASS.

---

### P0: Data race in importer upload response snapshot (`/api/upload`)

**Status**: DONE

**Impact**
- `make test-race` fails and the initial job JSON returned from `/api/upload` can be inconsistent under concurrency.

**Evidence**
- `internal/importer/server/server.go` `handleUpload` started `runImportJob` in a goroutine before returning `snapshotJob(job)` in the HTTP response.
- Repro (before fix): `make test-race` failed (race detected during `internal/importer/server.TestSkipsNonNumericValues`).

**Fix**
- `handleUpload`: take a snapshot before starting the async goroutine and return that snapshot.

**Verification**
- `make test-race`: PASS.

---

### P1: `CheckExportAPI` leaks response bodies (connection leak on success path)

**Impact**
- Successful export availability checks leak HTTP connections until GC, and can exhaust idle connections over time.

**Evidence**
- `internal/application/services/vm_service.go:499-521` calls `client.Export(...)` and ignores the returned `io.ReadCloser` when `err == nil`.
- `internal/infrastructure/vm/client.go:169-200` returns `resp.Body` for streaming, which must be closed by the caller.

**Suggested fix**
- If `client.Export(...)` returns a reader with `err == nil`, close it immediately (optionally read/discard a small amount if needed to validate format).
- Consider changing `Client.Export` to optionally “probe” the endpoint with a HEAD/GET-equivalent if VM supports it, to avoid opening a streaming body for a capability check.

---

### P1: Regex injection / query correctness risk when building `{job=~"..."}`

**Impact**
- Jobs with regex metacharacters can change selector semantics and lead to incorrect series estimates.
- In the worst case, this can create heavy regexes and slow queries (DoS-like behavior against the target VM).

**Evidence**
- Unescaped regex building:
  - `internal/application/services/vm_service.go:217-220` (`jobRegex := strings.Join(jobs, "|")`)
  - `internal/application/services/vm_service.go:250-252`
  - `internal/application/services/vm_service.go:281-283`
- Correct escaping exists elsewhere:
  - `internal/application/services/vm_service.go:475-484` uses `regexp.QuoteMeta`.

**Suggested fix**
- Reuse `buildJobFilterSelector` consistently or apply `regexp.QuoteMeta` for every job value before joining.
- Add a unit test where a job contains `.` or `|` and ensure selector matches the literal value.

---

### P1: Canceled jobs are never removed by retention cleanup

**Impact**
- Canceled jobs remain in memory indefinitely (until process restart), which can clutter status views and increase memory usage over time.

**Evidence**
- `internal/server/export_jobs.go:340-347` cleanup only removes `completed` and `failed`, but not `canceled`.

**Suggested fix**
- Include `JobCanceled` in cleanup retention logic.
- Consider also removing `pending` jobs that never started (if the system ever creates them).

---

### P1: Hard-coded 15 minute job timeout can kill legitimate exports

**Impact**
- Large time ranges (or slow endpoints) can exceed 15 minutes, causing the export to be canceled even though the user expects it to run.

**Evidence**
- `internal/server/export_jobs.go:109-110` uses `context.WithTimeout(..., 15*time.Minute)` for new jobs.
- `internal/server/export_jobs.go:158-159` applies the same limit to resumed jobs.

**Suggested fix**
- Prefer no hard timeout (use explicit cancel) or make it configurable via flag/env.
- If a timeout is required, compute it from `TotalBatches * defaultBatchTimeout + overhead`.

---

### P2: `/api/fs/*` endpoints enlarge security surface (especially if bound to non-localhost)

**Impact**
- `GET /api/fs/list` and `POST /api/fs/check` allow listing arbitrary directories and creating directories on the server host.
- Today `vmgather` defaults to `localhost:8080`, but users can bind to `0.0.0.0` via `-addr`, turning these into remote-accessible primitives.
- Even for localhost-only, this expands the “local web app” attack surface (CSRF-style side effects, privacy concerns).

**Evidence**
- Endpoint registration: `internal/server/server.go:136-137`
- Directory listing: `internal/server/server.go:984-1064` (accepts any `path`, resolves to abs, lists directories).
- Directory check + optional create: `internal/server/server.go:1066-1147` (supports `ensure=true` and does `os.MkdirAll`).
- Existing security test only covers download traversal:
  - `internal/server/security_test.go:11-78`

**Suggested fix**
- Strongly gate these endpoints behind an explicit opt-in flag (or build tag) and document the risk.
- If kept, restrict to a safe base directory (e.g., under user home) and/or require a per-session CSRF token from the UI.
- Add tests for “FS endpoints are disabled by default” (if the product direction is to limit them).

---

### P2: Debug/diagnostic logging is noisy by default

**Impact**
- Customers running the binary may see verbose logs that aren’t actionable, which complicates support instructions (“paste logs”).

**Evidence**
- `internal/server/server.go:195-207` logs connection details unconditionally (not gated by `s.debug`).

**Suggested fix**
- Gate these logs behind `s.debug` (or a verbose level).
- Ensure sensitive fields are never printed (currently it logs booleans, which is OK, but the default verbosity is the bigger issue).

---

### P2: Documentation inconsistencies (customer-facing confusion)

**Issues**
- Debug env var mentioned but not implemented:
  - `docs/development.md:139` references `VMGATHER_LOG=debug`, but no such env var exists in the codebase.
- E2E in CI is described as “disabled”, but a Playwright smoke job exists:
  - `tests/README.md:147-151` says “E2E tests are disabled in CI”.
  - `.github/workflows/main.yml:76-104` runs `e2e-smoke` (`npm run test:smoke`).
- Makefile states `local-test-env` is gitignored, but only files inside are ignored:
  - `Makefile:378-381` prints “This directory is gitignored”.
  - `.gitignore` ignores `local-test-env/testconfig` and `local-test-env/.env.dynamic` (not the directory).

**Suggested fix**
- Align wording in `docs/development.md`, `tests/README.md`, and Makefile messaging with actual behavior.
- Add a “Docs sanity” checklist item for releases (docs vs CI targets).

---

## Documentation: “customer-first” simplification suggestions

The docs are already fairly strong, but the first 30 seconds can be made even clearer by adding a single “Choose your workflow” table near the top of `README.md` and `docs/user-guide.md`.

Suggested table (concept):

- **Cluster metrics (UI)**: default support bundle for VictoriaMetrics components (vmagent/vmstorage/vminsert/vmselect/vmalert/vmsingle).
- **Selector/Query (UI)**: advanced mode for targeted selectors or MetricsQL, plus optional per-job selection for selectors.
- **Oneshot CLI (experimental)**: automation/testing; can stream JSONL to stdout (`-export-stdout`) for piping into scripts.
- **VMImport (UI)**: replay a bundle back into VictoriaMetrics; use for reproducing issues or validating bundle contents.

Also add one explicit security sentence:
- “If you bind `-addr 0.0.0.0:...`, you expose the UI and API to the network; run behind a trusted network only.”

## Maintainability (KISS/SOLID notes)

These are not “bugs”, but they are worth tracking:

- `internal/server/server.go` is a large, multi-responsibility file (routing, validation, export orchestration, fs helpers, download security, config). Consider splitting by API surface (validate/discover/export/fs/download).
- `cmd/vmgather/main.go` mixes UI server concerns and oneshot CLI execution in one `main`. If oneshot grows, consider a thin `main` + `internal/cmd` commands.
- `cmd/vmgather/main.go` and `cmd/vmimporter/main.go` duplicate `ensureAvailablePort` / `openBrowser`; could be extracted into a small internal package.

## Suggested test gaps

- Resume progress correctness (see P0 above).
- Race test for `ExportJobManager` (see P1 above).
- Version ldflags override test (see P0 above).
- Security tests for `/api/fs/*` if these endpoints remain enabled.

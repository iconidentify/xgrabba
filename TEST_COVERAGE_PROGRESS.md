# XGrabba Test Coverage Progress

## Current Status

**Overall Coverage: 16.8%** → **Target: 70%**

## Plan Document

The comprehensive plan has been created at:
`~/.claude/plans/xgrabba-test-coverage-70-percent.md`

## CI/CD Updates

✅ **Updated** `.github/workflows/ci.yml`: COVERAGE_THRESHOLD = 70
✅ **Updated** `Makefile`: COVERAGE_THRESHOLD = 70

**Note**: CI will now fail until we reach 70% coverage. This is intentional to enforce the target.

## Existing Test Files (Restored from Stash)

The following test files are already in the repository:

### High Coverage Packages (✅ Done)
- `internal/domain/domain_test.go` - 98.7% coverage
- `internal/config/config_test.go` - 97.0% coverage
- `cmd/xgrabba-tui/internal/config/config_test.go` - 100.0% coverage
- `internal/worker/pool_test.go` - 74.5% coverage

### Medium Coverage Packages (🔶 Needs Work)
- `internal/api/middleware/auth_test.go` - 58.5% (needs +11.5%)
- `internal/downloader/http_downloader_test.go` - 57.7% (needs +12.3%)
- `internal/repository/job_repository_test.go` - 54.7% (needs +15.3%)
- `internal/repository/playlist_repository_test.go` - Part of repository package
- `pkg/grok/client_test.go` - 59.6% (needs +10.4%)
- `pkg/whisper/client_test.go` - 38.7% (needs +31.3%)

### Low Coverage Packages (🔴 Needs Significant Work)
- `internal/api/handler/health_test.go` - 7.2% (needs +62.8%)
- `internal/api/handler/export_test.go` - Part of handler package
- `internal/api/handler/ui_test.go` - Part of handler package
- `internal/api/handler/testhelpers_test.go` - Test utilities
- `internal/service/export_service_test.go` - 14.4% (needs +55.6%)
- `internal/service/playlist_service_test.go` - Part of service package
- `internal/service/tweet_service_test.go` - Part of service package
- `internal/service/event_service_test.go` - Part of service package
- `internal/bookmarks/monitor_test.go` - 20.8% (needs +49.2%)
- `internal/bookmarks/store_test.go` - Part of bookmarks package
- `pkg/twitter/*_test.go` - 22.3% (needs +47.7%) - **MISSING**

### Zero Coverage Packages (🔴 Critical)
- `cmd/export/*_test.go` - **MISSING**
- `cmd/server/*_test.go` - **MISSING**
- `cmd/usb-manager/*_test.go` - **MISSING**
- `cmd/xgrabba-tui/*_test.go` - **MISSING**
- `cmd/xgrabba-tui/internal/ui/*_test.go` - **MISSING**
- `cmd/xgrabba-tui/internal/k8s/*_test.go` - **MISSING** (partial exists)
- `cmd/xgrabba-tui/internal/ssh/*_test.go` - **MISSING** (partial exists)
- `pkg/ffmpeg/*_test.go` - **MISSING**
- `pkg/usbclient/*_test.go` - **MISSING**
- `pkg/usbmanager/*_test.go` - **MISSING**

## Next Steps (Priority Order)

### Phase 1: Quick Wins (Target: 25% overall)
1. **internal/api/middleware** - Add 5-10 tests for edge cases
2. **internal/downloader** - Add 8-12 tests for retry/error handling
3. **internal/repository** - Add 10-15 tests for error paths
4. **pkg/crypto** - Add 5-8 tests for edge cases
5. **pkg/grok** - Add 5-8 tests for error handling

### Phase 2: Medium Effort (Target: 45% overall)
6. **cmd/xgrabba-tui/internal/github** - Add 10-15 tests
7. **pkg/whisper** - Add 15-20 tests
8. **internal/bookmarks** - Add 20-25 tests
9. **pkg/twitter** - Create test files, add 25-30 tests

### Phase 3: High Effort (Target: 60% overall)
10. **internal/service** - Expand existing tests, add 40-50 tests
11. **cmd/xgrabba-tui/internal/k8s** - Create/expand tests, add 30-40 tests
12. **internal/api/handler** - Expand existing tests, add 50-60 tests

### Phase 4: Critical Path (Target: 70% overall)
13. **cmd/xgrabba-tui/internal/ssh** - Create/expand tests, add 15-20 tests
14. **cmd/viewer** - Create tests, add 10-15 tests
15. **cmd/export** - Create tests, add 15-20 tests
16. **cmd/server** - Create tests, add 10-15 tests
17. **pkg/usbmanager** - Create tests, add 20-25 tests
18. **pkg/usbclient** - Create tests, add 10-15 tests
19. **pkg/ffmpeg** - Create tests, add 10-15 tests

## Running Tests

```bash
# Run all tests with coverage
make test-coverage

# Check if coverage meets threshold
make test-coverage-check

# View coverage report
make test-coverage-report

# Or directly:
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## CI/CD Status

The CI workflow will now:
1. Run all tests with coverage
2. Check if coverage >= 70%
3. **Fail the build** if coverage is below 70%
4. Upload coverage report as artifact

This ensures we maintain the 70% target going forward.

## Notes

- The plan document has detailed breakdowns for each package
- Focus on business logic and error handling for maximum impact
- Some packages (like cmd/server) are entry points and may require integration tests
- UI components may be challenging to unit test - consider integration tests
- USB manager packages will need hardware mocking

# Tasks: Cortex TUI Full Redesign

Board: tui-redesign | 20 tasks in 5 waves + wave 2' | change_id tui-redesign | spec_plane hybrid | workload_policy flexible | workflow sdd-lite. Wave order: W1 -> {W2, W2'} -> W3 -> W4 -> W5; W2 is a sequential chain on internal/tui/update.go (single lease); W2' is a sequential chain on internal/tui/view_knowledge.go parallel to W2; W3/W4 tasks are file-disjoint internally. No successor boards: blocked tasks decompose in place with cortex_ia_work_decompose. All Verify lines are raw, standalone command lines.

Rules carried by every task: allowed_files is 1-3 files and edits outside it are forbidden (escalate via in-place decomposition); pure-test tasks are anti-decomposition and defect-blocked; fast-TDD-eligible code tasks carry the Mutation Evidence Gate of cortex-work-protocol.md section 4/8 - a SURVIVED mutant blocks transition to in_review; source <= 700 LOC and tests <= 1200 LOC per task under the flexible policy.

Cross-board coordination: in-flight task tui-sync-fix-001 (board default, in progress) holds leases on internal/tui/update.go, internal/tui/store.go, internal/tui/update_test.go, internal/tui/store_test.go and defines the ReloadConfig/save semantics consumed by REQ-CFG-003/REQ-CFG-004 and REQ-SYNC-004. Tasks 004-008, 011 (store.go), and 020 (update_test.go) MUST NOT start until tui-sync-fix-001 reaches done. Tasks 001-003, 009-010, 012-019 use only unleased files and MAY start earlier, subject to DAG dependencies.

## 1. Wave 1 - Contracts & foundations (parallel, new files only)

### 1.1 tui-redesign-001 keymap-registry-contract
Requirements: REQ-NAV-001, REQ-NAV-002
Files: internal/tui/keymap.go, internal/tui/keymap_test.go
Verify: go test -count=1 ./internal/tui/ -run TestKeymapRegistry
Depends: none

### 1.2 tui-redesign-002 navigation-stack-contract
Requirements: REQ-NAV-003
Files: internal/tui/nav.go, internal/tui/nav_test.go
Verify: go test -count=1 ./internal/tui/ -run TestNavStack
Depends: none

### 1.3 tui-redesign-003 honest-status-vocabulary
Requirements: REQ-CFG-003, REQ-SYNC-003, REQ-SYNC-004
Files: internal/tui/status.go, internal/tui/status_test.go
Verify: go test -count=1 ./internal/tui/ -run TestStatusVocabulary
Depends: none

## 2. Wave 2 - Key-handler extraction chain + router removal (sequential on update.go)

### 2.1 tui-redesign-004 extract-dashboard-help-setup-handlers
Requirements: REQ-NAV-001
Files: internal/tui/update.go, internal/tui/keys_dashboard.go
Verify: go test -count=1 ./internal/tui/
Depends: tui-redesign-001

### 2.2 tui-redesign-005 extract-search-handlers
Requirements: REQ-NAV-001
Files: internal/tui/update.go, internal/tui/keys_search.go
Verify: go test -count=1 ./internal/tui/
Depends: tui-redesign-004

### 2.3 tui-redesign-006 extract-detail-timeline-sessions-handlers
Requirements: REQ-NAV-001
Files: internal/tui/update.go, internal/tui/keys_detail.go, internal/tui/keys_sessions.go
Verify: go test -count=1 ./internal/tui/
Depends: tui-redesign-005

### 2.4 tui-redesign-007 extract-system-config-handlers
Requirements: REQ-NAV-001
Files: internal/tui/update.go, internal/tui/keys_system.go, internal/tui/keys_config.go
Verify: go test -count=1 ./internal/tui/
Depends: tui-redesign-006

### 2.5 tui-redesign-008 remove-global-router-install-registry-dispatch
Requirements: REQ-NAV-001, REQ-NAV-002
Files: internal/tui/update.go, internal/tui/update_test.go
Verify: go test -count=1 ./internal/tui/ -run TestRegistryDispatch
Depends: tui-redesign-007

## 3. Wave 2' - View split chain (parallel to Wave 2, sequential on view_knowledge.go)

### 3.1 tui-redesign-009 split-views-search
Requirements: REQ-NAV-001
Files: internal/tui/view_knowledge.go, internal/tui/view_search.go
Verify: go test -count=1 ./internal/tui/
Depends: none

### 3.2 tui-redesign-010 split-views-detail-sessions
Requirements: REQ-NAV-001, REQ-NAV-003
Files: internal/tui/view_knowledge.go, internal/tui/view_detail.go, internal/tui/view_sessions.go
Verify: go test -count=1 ./internal/tui/
Depends: tui-redesign-009

## 4. Wave 3 - Parallel screen migrations (mutually disjoint writable files)

### 4.1 tui-redesign-011 dashboard-ia-dedupe-honest-chip
Requirements: REQ-NAV-005, REQ-SYNC-003
Files: internal/tui/view_dashboard.go, internal/tui/keys_dashboard.go, internal/tui/store.go
Verify: go test -count=1 ./internal/tui/ -run TestDashboardIA
Depends: tui-redesign-008

### 4.2 tui-redesign-012 search-results-preview-migration
Requirements: REQ-NAV-001, REQ-NAV-003
Files: internal/tui/view_search.go, internal/tui/keys_search.go
Verify: go test -count=1 ./internal/tui/ -run TestSearchKeymap
Depends: tui-redesign-008, tui-redesign-009

### 4.3 tui-redesign-013 detail-timeline-archive-migration
Requirements: REQ-NAV-001, REQ-NAV-003
Files: internal/tui/view_detail.go, internal/tui/keys_detail.go, internal/tui/view_knowledge.go
Verify: go test -count=1 ./internal/tui/ -run TestDetailTimelineKeymap
Depends: tui-redesign-008, tui-redesign-010

### 4.4 tui-redesign-014 graph-health-migration
Requirements: REQ-NAV-004
Files: internal/tui/view_graph.go, internal/tui/view_health.go, internal/tui/keys_system.go
Verify: go test -count=1 ./internal/tui/ -run TestSystemKeymap
Depends: tui-redesign-008

### 4.5 tui-redesign-015 sessions-migration
Requirements: REQ-NAV-001
Files: internal/tui/view_sessions.go, internal/tui/keys_sessions.go
Verify: go test -count=1 ./internal/tui/ -run TestSessionsKeymap
Depends: tui-redesign-008, tui-redesign-010

## 5. Wave 4 - Config unification, Connect flow, onboarding, help

### 5.1 tui-redesign-016 unified-configuration-screen
Requirements: REQ-CFG-001, REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-CFG-005, REQ-CFG-006
Files: internal/tui/view_control.go, internal/tui/keys_config.go
Verify: go test -count=1 ./internal/tui/ -run TestUnifiedConfig
Depends: tui-redesign-003, tui-redesign-008

### 5.2 tui-redesign-017 connect-guided-flow
Requirements: REQ-SYNC-001, REQ-SYNC-002, REQ-CFG-004
Files: internal/tui/view_connect.go, internal/tui/keys_connect.go, internal/tui/model.go
Verify: go test -count=1 ./internal/tui/ -run TestConnectFlow
Depends: tui-redesign-003, tui-redesign-016

### 5.3 tui-redesign-018 onboarding-empty-states
Requirements: REQ-SYNC-005
Files: internal/tui/view_empty.go, internal/tui/empty_state.go, internal/tui/view_dashboard.go
Verify: go test -count=1 ./internal/tui/ -run TestOnboardingEmptyStates
Depends: tui-redesign-011, tui-redesign-016, tui-redesign-017

### 5.4 tui-redesign-019 help-from-registry-palette-doc
Requirements: REQ-NAV-004
Files: internal/tui/view_modals.go, internal/tui/keys_dashboard.go
Verify: go test -count=1 ./internal/tui/ -run TestHelpFromRegistry
Depends: tui-redesign-008, tui-redesign-011

## 6. Wave 5 - Integration regression

### 6.1 tui-redesign-020 integration-regression-coverage-oracle
Requirements: REQ-NAV-001, REQ-NAV-002, REQ-NAV-004
Files: internal/tui/keys_coverage_test.go, internal/tui/model_test.go, internal/tui/update_test.go
Verify: go test -count=1 ./internal/tui/...
Depends: tui-redesign-011, tui-redesign-012, tui-redesign-013, tui-redesign-014, tui-redesign-015, tui-redesign-016, tui-redesign-017, tui-redesign-018, tui-redesign-019

Non-goals (intent preservation): no Go behavior changes outside internal/tui/**; no sync protocol, server endpoint, or internal/app/internal/config semantic changes; no implementation of removed no-ops; no user-remappable keybindings; no HTML prototypes.

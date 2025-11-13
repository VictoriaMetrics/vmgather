#!/bin/bash
# VMExporter - Pre-Release Cleanup Script
# Run this before publishing to GitHub

set -e  # Exit on error

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "VMExporter - Pre-Release Cleanup"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

ERRORS=0
WARNINGS=0

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
error() {
    echo -e "${RED}❌ ERROR: $1${NC}"
    ((ERRORS++))
}

warning() {
    echo -e "${YELLOW}⚠️  WARNING: $1${NC}"
    ((WARNINGS++))
}

success() {
    echo -e "${GREEN}✅ $1${NC}"
}

info() {
    echo "ℹ️  $1"
}

# =============================================================================
# PHASE 1: Delete temporary files
# =============================================================================

echo "Phase 1: Removing temporary files..."
echo ""

# 1.1 Delete log files
if [ -f "vmexporter.log" ]; then
    rm -f vmexporter.log
    success "Deleted vmexporter.log"
else
    info "No vmexporter.log found"
fi

rm -f *.log 2>/dev/null && success "Deleted *.log files" || info "No additional .log files"

# 1.2 Delete backup files
if [ -f "internal/server/static/app.js.backup" ]; then
    rm -f internal/server/static/app.js.backup
    success "Deleted app.js.backup"
fi

if [ -f "internal/server/static/index.html.backup" ]; then
    rm -f internal/server/static/index.html.backup
    success "Deleted index.html.backup"
fi

if [ -f "internal/server/server.go.bak" ]; then
    rm -f internal/server/server.go.bak
    success "Deleted server.go.bak"
fi

# Delete any other .bak or .backup files
find . -name "*.bak" -o -name "*.backup" | while read -r file; do
    if [[ ! "$file" =~ node_modules ]]; then
        rm -f "$file"
        success "Deleted $file"
    fi
done

echo ""

# =============================================================================
# PHASE 2: Organize internal documentation
# =============================================================================

echo "Phase 2: Organizing internal documentation..."
echo ""

# 2.1 Create local-development/reports if needed
mkdir -p local-development/reports

# 2.2 Move Russian reports
if [ -f "reports/ФИНАЛЬНЫЙ_РЕПОРТ_RU.md" ]; then
    mv reports/ФИНАЛЬНЫЙ_РЕПОРТ_RU.md local-development/reports/
    success "Moved ФИНАЛЬНЫЙ_РЕПОРТ_RU.md to local-development/reports/"
fi

if [ -f "reports/metrics-analysis.json" ]; then
    mv reports/metrics-analysis.json local-development/reports/
    success "Moved metrics-analysis.json to local-development/reports/"
fi

# 2.3 Move internal tracking docs
if [ -f "OPENSOURCE_PROGRESS.md" ]; then
    mv OPENSOURCE_PROGRESS.md local-development/
    success "Moved OPENSOURCE_PROGRESS.md to local-development/"
fi

if [ -f "OPENSOURCE_TODO.md" ]; then
    mv OPENSOURCE_TODO.md local-development/
    success "Moved OPENSOURCE_TODO.md to local-development/"
fi

# 2.4 Keep METRICS_ANALYSIS_REPORT.md for now (English, could be useful)
if [ -f "reports/METRICS_ANALYSIS_REPORT.md" ]; then
    info "Keeping reports/METRICS_ANALYSIS_REPORT.md (English analysis)"
fi

echo ""

# =============================================================================
# PHASE 3: Update .gitignore
# =============================================================================

echo "Phase 3: Updating .gitignore..."
echo ""

# Check if entries already exist
if ! grep -q "^# Log files" .gitignore; then
    cat >> .gitignore << 'EOF'

# Log files  
*.log
*.bak
*.backup

# Reports (internal analysis)
reports/
EOF
    success "Updated .gitignore with log files and reports"
else
    info ".gitignore already contains log file rules"
fi

echo ""

# =============================================================================
# PHASE 4: Security checks
# =============================================================================

echo "Phase 4: Security verification..."
echo ""

# 4.1 Check for Russian text in public docs
if grep -r '[А-Яа-я]' README.md CONTRIBUTING.md SECURITY.md CHANGELOG.md docs/*.md 2>/dev/null; then
    error "Found Russian text in public documentation!"
else
    success "No Russian text in public documentation"
fi

# 4.2 Check for hardcoded credentials (basic check)
CRED_COUNT=$(grep -r "password.*=" --include="*.go" --include="*.js" internal/ cmd/ 2>/dev/null | grep -v "Password string" | grep -v "// " | wc -l | tr -d ' ')
if [ "$CRED_COUNT" -gt 0 ]; then
    warning "Found $CRED_COUNT potential hardcoded credentials (review manually)"
else
    success "No obvious hardcoded credentials found"
fi

# 4.3 Verify local folders will be ignored
if [ ! -d ".git" ]; then
    warning "Not a git repository yet - cannot verify .gitignore"
else
    if git check-ignore local-development >/dev/null 2>&1; then
        success "local-development/ will be ignored by git"
    else
        error "local-development/ NOT ignored by git!"
    fi
    
    if git check-ignore local-test-env >/dev/null 2>&1; then
        success "local-test-env/ will be ignored by git"
    else
        error "local-test-env/ NOT ignored by git!"
    fi
fi

echo ""

# =============================================================================
# PHASE 5: Final verification
# =============================================================================

echo "Phase 5: Final verification..."
echo ""

# 5.1 Check critical files exist
for file in LICENSE README.md CHANGELOG.md CONTRIBUTING.md SECURITY.md; do
    if [ -f "$file" ]; then
        success "$file exists"
    else
        error "$file MISSING!"
    fi
done

# 5.2 Check docs folder
if [ -d "docs" ]; then
    success "docs/ folder exists"
    for doc in architecture.md user-guide.md development.md; do
        if [ -f "docs/$doc" ]; then
            success "docs/$doc exists"
        else
            warning "docs/$doc missing"
        fi
    done
else
    error "docs/ folder MISSING!"
fi

# 5.3 Check .github structure
if [ -d ".github/workflows" ]; then
    success ".github/workflows/ exists"
else
    warning ".github/workflows/ missing (CI will not run)"
fi

echo ""

# =============================================================================
# Summary
# =============================================================================

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Cleanup Summary"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

if [ $ERRORS -eq 0 ] && [ $WARNINGS -eq 0 ]; then
    echo -e "${GREEN}✅ ALL CHECKS PASSED!${NC}"
    echo ""
    echo "Repository is ready for publication."
    echo ""
    echo "Next steps:"
    echo "1. Run tests: make test"
    echo "2. Build binaries: make build-all"
    echo "3. Initialize git: git init (if not done)"
    echo "4. Review changes: git status"
    echo "5. Follow FINAL_RELEASE_CHECKLIST.md"
    echo ""
    exit 0
elif [ $ERRORS -eq 0 ]; then
    echo -e "${YELLOW}⚠️  CLEANUP COMPLETE WITH $WARNINGS WARNING(S)${NC}"
    echo ""
    echo "Review warnings above before proceeding."
    echo ""
    exit 0
else
    echo -e "${RED}❌ CLEANUP FAILED: $ERRORS ERROR(S), $WARNINGS WARNING(S)${NC}"
    echo ""
    echo "Fix errors above before publication."
    echo ""
    exit 1
fi


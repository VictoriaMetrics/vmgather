const JOB_CHUNK_SIZE_HINT = 512 * 1024;
const DEFAULT_MAX_LABELS_OVERRIDE = 40;
const JOB_POLL_INTERVAL_MS = 1200;
const JOB_POLL_REQUEST_TIMEOUT_MS = 15000;
const JOB_POLL_MAX_ERRORS = 5;
const JOB_PROGRESS_STALL_MS = 45000;
const dropZone = document.getElementById('dropZone');
const bundleInput = document.getElementById('bundleFile');
const statusPanel = document.getElementById('statusPanel');
const fileHint = document.getElementById('fileHint');
const startButton = document.getElementById('startImportBtn');
let connectionValid = false;
let analysisReady = false;
let totalBatches = 0;
let uploadStart = 0;
let selectedBundleBytes = 0;
let currentJobId = null;
let jobPollTimer = null;
let jobPollErrors = 0;
let lastJobUpdateAtMs = 0;
let lastJobUpdatedToken = '';
let lastKnownJobId = null;
let lastKnownJobSnapshot = null;
let stallWarningShown = false;
let uploadingBundle = false;
let lastAnalysisSummary = null;
let lastAnalysisMode = 'sample';
let lastMaxLabelsLimit = 0;
let lastMaxLabelsSource = 'unknown';
let lastLabelSimulation = null;
let recentProfiles = [];
let protectedDropLabels = [];
let selectedDropLabels = new Set();
const MAX_LABEL_DROP_SUGGESTIONS = 5;

document.addEventListener('DOMContentLoaded', () => {
    protectedDropLabels = ['__name__', 'job', 'instance'];
    toggleAuthFields();
    toggleTenantInput();
    initializeUrlValidation();
    initializeMetricStepSelector();
    initializeMaxLabelsOverrideInput();
    ensureDefaultMaxLabelsOverride();
    initDropZone();
    renderLabelManager([], 0);
    loadRecentProfiles();
    refreshStartButton();
    refreshPreflightControls();
});

function initDropZone() {
    if (!dropZone || !bundleInput) return;

    ['dragenter', 'dragover'].forEach(evt => dropZone.addEventListener(evt, event => {
        event.preventDefault();
        event.stopPropagation();
        dropZone.classList.add('dragover');
    }));
    ['dragleave', 'drop'].forEach(evt => dropZone.addEventListener(evt, event => {
        event.preventDefault();
        event.stopPropagation();
        dropZone.classList.remove('dragover');
    }));

    dropZone.addEventListener('click', () => bundleInput.click());
    dropZone.addEventListener('drop', event => {
        const files = event.dataTransfer.files;
        if (files.length) {
            bundleInput.files = files;
            dropZone.querySelector('strong').textContent = files[0].name;
            updateFileHint(files[0]);
            applyRecommendedMetricStep(false);
            analyzeBundle();
        }
    });

    bundleInput.addEventListener('change', () => {
        const file = bundleInput.files[0];
        dropZone.querySelector('strong').textContent = file ? file.name : 'Drop file here';
        updateFileHint(file);
        applyRecommendedMetricStep(false);
        analyzeBundle();
    });
}

function updateFileHint(file) {
    if (!fileHint) return;
    if (!file) {
        fileHint.textContent = '';
        selectedBundleBytes = 0;
        clearAnalysisResult();
        return;
    }
    selectedBundleBytes = file.size;
    const size = formatBytes(file.size);
    fileHint.textContent = `Selected ${file.name} (${size})`;
    clearAnalysisResult();
    setAnalysisReady(false);
    refreshPreflightControls();
}

function getManualMaxLabelsOverride() {
    const input = document.getElementById('maxLabelsOverride');
    if (!input) return DEFAULT_MAX_LABELS_OVERRIDE;
    const raw = String(input.value || '').trim();
    if (!raw) return DEFAULT_MAX_LABELS_OVERRIDE;
    const value = Number(raw);
    if (!Number.isFinite(value) || value <= 0 || !Number.isInteger(value)) return DEFAULT_MAX_LABELS_OVERRIDE;
    return value;
}

function ensureDefaultMaxLabelsOverride(force = false) {
    const input = document.getElementById('maxLabelsOverride');
    if (!input) return;
    const raw = String(input.value || '').trim();
    if (force || !raw) {
        input.value = String(DEFAULT_MAX_LABELS_OVERRIDE);
    }
}

function clearMaxLabelsRisk() {
    const risk = document.getElementById('maxLabelsRisk');
    if (!risk) return;
    risk.style.display = 'none';
    risk.textContent = '';
}

function updateMaxLabelsRisk(summary = lastAnalysisSummary, maxLabelsLimit = lastMaxLabelsLimit, source = lastMaxLabelsSource) {
    const risk = document.getElementById('maxLabelsRisk');
    if (!risk) return;
    if (!summary) {
        clearMaxLabelsRisk();
        return;
    }
    const selected = getSelectedDropLabels();
    const impact = evaluateDropImpact(lastLabelSimulation, selected, maxLabelsLimit);
    const maxSeen = Number(summary.max_labels_seen || 0);
    const fallbackOverLimit = Number(summary.over_label_limit || 0);
    const overLimit = impact ? impact.overSeries : fallbackOverLimit;
    const pointsAtRisk = impact ? impact.overPoints : Number(summary.over_limit_points || 0);
    const maxAfterDrop = impact ? impact.maxAfterDrop : maxSeen;
    const simulationInfo = impact ? ` (sample series analyzed: ${impact.seriesCount}${impact.capped ? ', capped' : ''})` : '';
    if (maxSeen <= 0) {
        clearMaxLabelsRisk();
        return;
    }
    if (source === 'unknown') {
        risk.className = 'status error';
        risk.style.display = 'block';
        risk.textContent = `Target maxLabelsPerTimeseries is unknown. Preflight observed up to ${maxSeen} labels per series (projected max after current drop selection: ${maxAfterDrop}). Set optional override and rerun preflight (or Full collection) for precise drop diagnostics before import.${simulationInfo}`;
        return;
    }
    if (maxLabelsLimit > 0 && overLimit > 0) {
        const extra = impact ? impact.minAdditionalDrops : 0;
        const suggestion = impact && impact.candidateLabels.length
            ? ` Candidate labels to drop first: ${impact.candidateLabels.join(', ')}.`
            : '';
        risk.className = 'status error';
        risk.style.display = 'block';
        risk.textContent = `Potential drops detected: projected max labels ${maxAfterDrop} exceeds limit ${maxLabelsLimit}. Over-limit series in sample: ${overLimit}; estimated sampled points at risk: ${pointsAtRisk}.${extra > 0 ? ` Need at least ${extra} more label drop(s) on worst affected series.` : ''}${suggestion}${simulationInfo}`;
        return;
    }
    if (maxLabelsLimit > 0) {
        risk.className = 'status success';
        risk.style.display = 'block';
        risk.textContent = `Current drop-label selection keeps sampled series within maxLabelsPerTimeseries=${maxLabelsLimit}. Projected max labels after drop: ${maxAfterDrop}.${simulationInfo}`;
        return;
    }
    clearMaxLabelsRisk();
}

function initializeMaxLabelsOverrideInput() {
    const input = document.getElementById('maxLabelsOverride');
    if (!input) return;
    input.addEventListener('input', () => {
        const raw = String(input.value || '').trim();
        if (!raw) {
            input.setCustomValidity('');
        } else {
            const value = Number(raw);
            if (!Number.isFinite(value) || value <= 0 || !Number.isInteger(value)) {
                input.setCustomValidity('Enter a positive integer');
            } else {
                input.setCustomValidity('');
            }
        }
        if (lastAnalysisSummary) {
            const currentLimit = getManualMaxLabelsOverride();
            const currentSource = currentLimit > 0 ? 'manual' : lastMaxLabelsSource;
            updateMaxLabelsRisk(lastAnalysisSummary, currentLimit, currentSource);
        } else {
            clearMaxLabelsRisk();
        }
    });
    input.addEventListener('blur', () => {
        ensureDefaultMaxLabelsOverride();
        if (lastAnalysisSummary) {
            const currentLimit = getManualMaxLabelsOverride();
            const currentSource = currentLimit > 0 ? 'manual' : lastMaxLabelsSource;
            updateMaxLabelsRisk(lastAnalysisSummary, currentLimit, currentSource);
        }
    });
}

function normalizeDropLabels(labels) {
    if (!Array.isArray(labels)) return [];
    const seen = new Set();
    const result = [];
    labels.forEach(label => {
        const value = String(label || '').trim();
        if (!value || seen.has(value)) return;
        seen.add(value);
        result.push(value);
    });
    result.sort((a, b) => a.localeCompare(b));
    return result;
}

function escapeHtml(value) {
    return String(value || '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

function getSelectedDropLabels() {
    return normalizeDropLabels(Array.from(selectedDropLabels));
}

function setSelectedDropLabels(labels) {
    selectedDropLabels = new Set(normalizeDropLabels(labels).filter(label => !protectedDropLabels.includes(label)));
}

function decodeBase64Raw(input) {
    if (!input) return new Uint8Array();
    const normalized = String(input).replace(/-/g, '+').replace(/_/g, '/');
    const padding = normalized.length % 4 === 0 ? '' : '='.repeat(4 - (normalized.length % 4));
    const binary = atob(normalized + padding);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) {
        bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
}

function hasBit(bitset, index) {
    if (!bitset || index < 0) return false;
    const byteIndex = Math.floor(index / 8);
    const bitIndex = index % 8;
    if (byteIndex >= bitset.length) return false;
    return (bitset[byteIndex] & (1 << bitIndex)) !== 0;
}

function buildLabelSimulation(summary = {}) {
    const universeRaw = Array.isArray(summary.label_universe) ? summary.label_universe : [];
    const universe = universeRaw.map(item => String(item || '').trim()).filter(Boolean);
    const encodedBitsets = Array.isArray(summary.series_label_bitsets) ? summary.series_label_bitsets : [];
    if (!universe.length || !encodedBitsets.length) {
        return null;
    }
    const labelCountsRaw = Array.isArray(summary.series_label_counts) ? summary.series_label_counts : [];
    const pointCountsRaw = Array.isArray(summary.series_point_counts) ? summary.series_point_counts : [];
    const seriesCount = Math.min(encodedBitsets.length, labelCountsRaw.length);
    if (seriesCount <= 0) {
        return null;
    }

    const bitsets = new Array(seriesCount);
    for (let i = 0; i < seriesCount; i += 1) {
        try {
            bitsets[i] = decodeBase64Raw(encodedBitsets[i]);
        } catch (err) {
            console.debug('Failed to decode label bitset', err);
            return null;
        }
    }

    const labelCounts = labelCountsRaw.slice(0, seriesCount).map(value => Number(value || 0));
    const pointCounts = pointCountsRaw.slice(0, seriesCount).map(value => Number(value || 0));
    while (pointCounts.length < seriesCount) pointCounts.push(0);

    const indexByName = new Map();
    universe.forEach((name, idx) => indexByName.set(name, idx));

    return {
        universe,
        indexByName,
        bitsets,
        labelCounts,
        pointCounts,
        seriesCount,
        capped: Boolean(summary.simulation_series_capped),
    };
}

function evaluateDropImpact(simulation, selectedLabels, maxLabelsLimit) {
    if (!simulation) return null;
    const selectedIndexes = [];
    selectedLabels.forEach(label => {
        const idx = simulation.indexByName.get(label);
        if (typeof idx === 'number') selectedIndexes.push(idx);
    });

    let overSeries = 0;
    let overPoints = 0;
    let maxAfterDrop = 0;
    let maxExcess = 0;
    const overSeriesIndexes = [];

    for (let i = 0; i < simulation.seriesCount; i += 1) {
        const bitset = simulation.bitsets[i];
        const original = Number(simulation.labelCounts[i] || 0);
        let dropped = 0;
        for (let j = 0; j < selectedIndexes.length; j += 1) {
            if (hasBit(bitset, selectedIndexes[j])) dropped += 1;
        }
        const after = Math.max(0, original - dropped);
        if (after > maxAfterDrop) maxAfterDrop = after;
        if (maxLabelsLimit > 0 && after > maxLabelsLimit) {
            const excess = after - maxLabelsLimit;
            overSeries += 1;
            overPoints += Number(simulation.pointCounts[i] || 0);
            overSeriesIndexes.push(i);
            if (excess > maxExcess) maxExcess = excess;
        }
    }

    const selectedIndexSet = new Set(selectedIndexes);
    const protectedIndexSet = new Set(
        normalizeDropLabels(protectedDropLabels)
            .map(label => simulation.indexByName.get(label))
            .filter(idx => typeof idx === 'number'),
    );
    const candidateScores = new Map();
    for (let k = 0; k < overSeriesIndexes.length; k += 1) {
        const seriesIndex = overSeriesIndexes[k];
        const bitset = simulation.bitsets[seriesIndex];
        for (let idx = 0; idx < simulation.universe.length; idx += 1) {
            if (!hasBit(bitset, idx) || selectedIndexSet.has(idx) || protectedIndexSet.has(idx)) {
                continue;
            }
            candidateScores.set(idx, (candidateScores.get(idx) || 0) + 1);
        }
    }
    const candidateLabels = Array.from(candidateScores.entries())
        .sort((a, b) => {
            if (a[1] === b[1]) {
                const left = simulation.universe[a[0]] || '';
                const right = simulation.universe[b[0]] || '';
                return left.localeCompare(right);
            }
            return b[1] - a[1];
        })
        .slice(0, MAX_LABEL_DROP_SUGGESTIONS)
        .map(([idx]) => simulation.universe[idx]);

    return {
        seriesCount: simulation.seriesCount,
        overSeries,
        overPoints,
        maxAfterDrop,
        minAdditionalDrops: Math.max(0, maxExcess),
        candidateLabels,
        capped: simulation.capped,
    };
}

function renderLabelManager(labelStats = [], totalLabels = 0) {
    const details = document.getElementById('labelManagerDetails');
    const summary = document.getElementById('labelManagerSummary');
    const rows = document.getElementById('labelManagerRows');
    const hint = document.getElementById('labelManagerHint');
    if (!details || !summary || !rows || !hint) return;

    const normalizedStats = Array.isArray(labelStats) ? labelStats : [];
    const shownLabels = normalizedStats.length;
    const detectedLabels = Math.max(shownLabels, Number(totalLabels || 0));
    const selectedLabels = getSelectedDropLabels();
    const selectedCount = selectedLabels.length;
    if (!normalizedStats.length) {
        details.open = false;
        summary.textContent = selectedCount > 0
            ? `Label management (drop selected: ${selectedCount}${detectedLabels > 0 ? `; total labels: ${detectedLabels}` : ''})`
            : 'Label management (optional)';
        const selectedLabelsEscaped = selectedLabels.map(label => `<code>${escapeHtml(label)}</code>`).join(', ');
        rows.innerHTML = selectedCount > 0
            ? `<div class="input-hint">Selected drop labels: ${selectedLabelsEscaped}.</div><div class="input-hint">Run preflight to preview all detected labels and tune selection.</div>`
            : '<div class="input-hint">Run preflight to see all detected labels and choose what to drop before import.</div>';
        let hintText = protectedDropLabels.length
            ? `Protected labels (always kept): ${protectedDropLabels.join(', ')}.`
            : 'Protected labels are always kept.';
        if (detectedLabels > 0) {
            hintText += ` Detected labels in sample: ${detectedLabels}.`;
        }
        hint.textContent = hintText;
        return;
    }

    setSelectedDropLabels(Array.from(selectedDropLabels));
    const selectedNow = getSelectedDropLabels();
    const selectedNowCount = selectedNow.length;
    const visibleSelected = normalizedStats.filter(item => selectedDropLabels.has(String(item.name || '').trim())).length;
    const hiddenSelected = Math.max(0, selectedNowCount - visibleSelected);
    summary.textContent = selectedNowCount > 0
        ? `Label management (drop selected: ${selectedNowCount}; total labels: ${detectedLabels})`
        : `Label management (total labels: ${detectedLabels})`;

    rows.innerHTML = normalizedStats.map(item => {
        const label = String(item.name || '');
        const count = Number(item.count || 0);
        const isProtected = protectedDropLabels.includes(label);
        const checked = !isProtected && selectedDropLabels.has(label);
        const safeLabel = escapeHtml(label);
        return `
            <label style="display:flex; align-items:center; gap:8px; margin-bottom:6px; opacity:${isProtected ? 0.7 : 1};">
                <input type="checkbox" data-drop-label="${safeLabel}" ${checked ? 'checked' : ''} ${isProtected ? 'disabled' : ''} style="width:auto;">
                <code>${safeLabel}</code>
                <span class="input-hint">seen in ${count} sample series${isProtected ? ' · protected' : ''}</span>
            </label>
        `;
    }).join('');

    rows.querySelectorAll('input[data-drop-label]').forEach(input => {
        input.addEventListener('change', event => {
            const label = event.target.getAttribute('data-drop-label');
            if (!label || protectedDropLabels.includes(label)) return;
            if (event.target.checked) {
                selectedDropLabels.add(label);
            } else {
                selectedDropLabels.delete(label);
            }
            renderLabelManager(normalizedStats, detectedLabels);
            updateMaxLabelsRisk(lastAnalysisSummary, lastMaxLabelsLimit, lastMaxLabelsSource);
        });
    });

    const protectedHint = protectedDropLabels.length
        ? `Protected labels (always kept): ${protectedDropLabels.join(', ')}.`
        : 'Protected labels are always kept.';
    const totalHint = detectedLabels > shownLabels
        ? ` Showing ${shownLabels} labels out of ${detectedLabels} detected labels in sample.`
        : ` Showing all ${shownLabels} detected labels in sample.`;
    hint.textContent = hiddenSelected > 0
        ? `${protectedHint} ${hiddenSelected} selected label(s) are not in the current preflight list but will still be dropped.${totalHint}`
        : `${protectedHint}${totalHint}`;
}

function resetForm() {
    document.getElementById('vmUrl').value = '';
    document.getElementById('tenantId').value = '';
    document.getElementById('authType').value = 'none';
    toggleAuthFields();
    const tenantToggle = document.getElementById('enableTenant');
    if (tenantToggle) {
        tenantToggle.checked = false;
    }
    toggleTenantInput();
    connectionValid = false;
    bundleInput.value = '';
    dropZone.querySelector('strong').textContent = 'Drop file here';
    updateFileHint(null);
    stopJobPolling();
    hideImportProgressPanel();
    statusPanel.style.display = 'none';
    fileHint.textContent = '';
    document.getElementById('vmUrlHint').textContent = 'Enter a VictoriaMetrics endpoint.';
    document.getElementById('vmUrlHint').classList.remove('success', 'error');
    ensureDefaultMaxLabelsOverride(true);
    totalBatches = 0;
    setSelectedDropLabels([]);
    renderLabelManager([], 0);
    setAnalysisReady(false);
    refreshStartButton();
    refreshPreflightControls();
    clearStatusActions();
    lastKnownJobId = null;
    lastKnownJobSnapshot = null;
    stallWarningShown = false;
}

function toggleAuthFields() {
    const authType = document.getElementById('authType').value;
    const authFields = document.getElementById('authFields');
    if (!authFields) return;

    let html = '';
    switch (authType) {
        case 'basic':
            html = `
                <div class="form-group">
                    <label for="username">Username:</label>
                    <input type="text" id="username" placeholder="basic auth username">
                </div>
                <div class="form-group">
                    <label for="password">Password:</label>
                    <input type="password" id="password" placeholder="••••••">
                </div>
            `;
            break;
        case 'bearer':
            html = `
                <div class="form-group">
                    <label for="token">Bearer Token:</label>
                    <input type="password" id="token" placeholder="token">
                </div>
            `;
            break;
        case 'header':
            html = `
                <div class="form-group">
                    <label for="headerName">Header Name:</label>
                    <input type="text" id="headerName" placeholder="X-API-Key">
                </div>
                <div class="form-group">
                    <label for="headerValue">Header Value:</label>
                    <input type="password" id="headerValue" placeholder="Value">
                </div>
            `;
            break;
        default:
            html = '';
    }
    authFields.innerHTML = html;
}

function toggleTenantInput() {
    const checkbox = document.getElementById('enableTenant');
    const tenantInput = document.getElementById('tenantId');
    if (!checkbox || !tenantInput) return;
    const enabled = checkbox.checked;
    tenantInput.disabled = !enabled;
    if (!enabled) {
        tenantInput.value = '';
    }
}

function initializeUrlValidation() {
    const input = document.getElementById('vmUrl');
    const hint = document.getElementById('vmUrlHint');
    if (!input || !hint) return;

    const applyState = () => {
        const assessment = analyzeVmUrl(input.value);
        if (assessment.valid) {
            hint.textContent = `[OK] ${assessment.message || 'URL looks good'}`;
            hint.classList.remove('error');
            hint.classList.add('success');
        } else {
            hint.textContent = `[FAIL] ${assessment.message || 'Invalid URL'}`;
            hint.classList.remove('success');
            hint.classList.add('error');
            connectionValid = false;
        }
    };

    input.addEventListener('input', applyState);
    applyState();
}

const PROTOCOL_REGEX = /^[a-zA-Z][a-zA-Z0-9+\-.]*:\/\//;

function analyzeVmUrl(rawUrl) {
    const trimmed = (rawUrl || '').trim();
    if (!trimmed) {
        return { valid: false, message: 'Enter a VictoriaMetrics URL' };
    }
    if (/[\\\s]/.test(trimmed)) {
        return { valid: false, message: 'Remove spaces or backslashes from the URL' };
    }
    let candidate = trimmed;
    if (!PROTOCOL_REGEX.test(candidate)) {
        candidate = `http://${candidate}`;
    }
    try {
        const parsedUrl = new URL(candidate);
        if (!['http:', 'https:'].includes(parsedUrl.protocol)) {
            return { valid: false, message: 'Only http:// or https:// supported' };
        }
        return { valid: true, message: `Target: ${parsedUrl.hostname}` };
    } catch (err) {
        return { valid: false, message: 'Invalid URL format' };
    }
}

function getConnectionConfig() {
    const urlValue = document.getElementById('vmUrl').value.trim();
    const authType = document.getElementById('authType').value;
    const tenantEnabled = document.getElementById('enableTenant')?.checked;
    const tenantValue = document.getElementById('tenantId').value.trim();
    return {
        url: urlValue,
        tenantId: tenantEnabled ? tenantValue : '',
        authType,
        username: document.getElementById('username')?.value.trim() || '',
        password: document.getElementById('password')?.value || '',
        token: document.getElementById('token')?.value || '',
        headerName: document.getElementById('headerName')?.value.trim() || '',
        headerValue: document.getElementById('headerValue')?.value || '',
        skipTls: document.getElementById('skipTls')?.checked || false,
    };
}

function formatRecentProfileLabel(profile) {
    const endpoint = profile.endpoint || 'unknown-endpoint';
    const tenant = profile.tenant_id ? `tenant ${profile.tenant_id}` : 'single-tenant';
    const auth = profile.auth_type || 'none';
    let host = endpoint;
    try {
        const candidate = PROTOCOL_REGEX.test(endpoint) ? endpoint : `http://${endpoint}`;
        host = new URL(candidate).host;
    } catch (err) {
        host = endpoint;
    }
    const when = profile.last_used_at ? new Date(profile.last_used_at).toLocaleString() : '';
    return when ? `${host} · ${tenant} · ${auth} · ${when}` : `${host} · ${tenant} · ${auth}`;
}

function renderRecentProfiles() {
    const select = document.getElementById('recentProfilesSelect');
    if (!select) return;

    const currentValue = select.value;
    select.innerHTML = '';

    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.textContent = recentProfiles.length ? 'Select recent profile…' : 'No recent profiles yet';
    select.appendChild(placeholder);

    recentProfiles.forEach(profile => {
        const option = document.createElement('option');
        option.value = profile.id || '';
        option.textContent = formatRecentProfileLabel(profile);
        select.appendChild(option);
    });

    if (currentValue && recentProfiles.some(profile => profile.id === currentValue)) {
        select.value = currentValue;
    } else {
        select.value = '';
    }
}

async function loadRecentProfiles() {
    try {
        const resp = await fetch('/api/profiles/recent');
        if (!resp.ok) {
            return;
        }
        const payload = await resp.json();
        recentProfiles = Array.isArray(payload.profiles) ? payload.profiles : [];
        renderRecentProfiles();
    } catch (err) {
        console.debug('Failed to load recent profiles', err);
    }
}

function applySelectedRecentProfile() {
    const select = document.getElementById('recentProfilesSelect');
    if (!select || !select.value) return;
    const profile = recentProfiles.find(candidate => candidate.id === select.value);
    if (!profile) return;

    const vmUrl = document.getElementById('vmUrl');
    if (vmUrl) {
        vmUrl.value = profile.endpoint || '';
        vmUrl.dispatchEvent(new Event('input', { bubbles: true }));
    }

    const enableTenant = document.getElementById('enableTenant');
    if (enableTenant) {
        enableTenant.checked = Boolean(profile.tenant_id);
        toggleTenantInput();
    }
    const tenantInput = document.getElementById('tenantId');
    if (tenantInput) {
        tenantInput.value = profile.tenant_id || '';
    }

    const authTypeInput = document.getElementById('authType');
    if (authTypeInput) {
        authTypeInput.value = profile.auth_type || 'none';
        toggleAuthFields();
    }

    if ((profile.auth_type || 'none') === 'basic') {
        const username = document.getElementById('username');
        const password = document.getElementById('password');
        if (username) username.value = profile.username || '';
        if (password) password.value = '';
    } else if ((profile.auth_type || 'none') === 'bearer') {
        const token = document.getElementById('token');
        if (token) token.value = '';
    } else if ((profile.auth_type || 'none') === 'header') {
        const headerName = document.getElementById('headerName');
        const headerValue = document.getElementById('headerValue');
        if (headerName) headerName.value = profile.custom_header_name || '';
        if (headerValue) headerValue.value = '';
    }

    const skipTls = document.getElementById('skipTls');
    if (skipTls) {
        skipTls.checked = Boolean(profile.skip_tls_verify);
    }

    const metricStep = document.getElementById('metricStep');
    if (metricStep) {
        const targetStep = String(profile.metric_step_seconds || '');
        const hasOption = Array.from(metricStep.options).some(option => option.value === targetStep);
        metricStep.value = hasOption ? targetStep : 'auto';
        applyRecommendedMetricStep(false);
    }

    const shiftInput = document.getElementById('timeShiftMinutes');
    if (shiftInput) {
        shiftInput.value = String(Math.round((profile.time_shift_ms || 0) / (60 * 1000)));
    }
    const maxLabelsOverride = document.getElementById('maxLabelsOverride');
    if (maxLabelsOverride) {
        maxLabelsOverride.value = profile.max_labels_override ? String(profile.max_labels_override) : String(DEFAULT_MAX_LABELS_OVERRIDE);
    }
    setSelectedDropLabels(profile.drop_labels || []);
    renderLabelManager(lastAnalysisSummary?.label_stats || [], lastAnalysisSummary?.total_labels || 0);

    connectionValid = false;
    refreshStartButton();
    showStatus('Profile applied. Re-enter secret fields and run Test Connection.', false);
}

async function testConnection() {
    const btn = document.getElementById('testConnectionBtn');
    const label = document.getElementById('testBtnText');
    const result = document.getElementById('connectionResult');
    if (!btn || !label || !result) return;

    label.innerHTML = '<span class="loading-spinner"></span> Testing...';
    result.className = 'status';
    result.style.display = 'block';
    result.textContent = 'Validating endpoint...';

    const config = serializeConfig();
    if (!config.endpoint) {
        result.classList.add('error');
        result.textContent = 'Endpoint URL is required';
        label.textContent = 'Test Connection';
        return;
    }

    try {
        // First attempt with TLS verification
        let resp = await fetch('/api/check-endpoint', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config),
        });
        let data = await resp.json().catch(() => ({}));

        // Auto-retry with skip TLS if certificate error
        if (!resp.ok && data.error && data.error.includes('certificate')) {
            result.textContent = 'TLS error detected, retrying without verification...';
            config.skip_tls_verify = true;
            resp = await fetch('/api/check-endpoint', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(config),
            });
            data = await resp.json().catch(() => ({}));
        }

        if (!resp.ok || data.ok !== true) {
            throw new Error(data.error || 'Endpoint rejected request');
        }
        result.classList.remove('error');
        result.classList.add('success');
        result.textContent = config.skip_tls_verify
            ? 'Connection successful (TLS verification skipped for self-signed cert)'
            : 'Connection successful! Uploads can start.';
        connectionValid = true;
        await loadRecentProfiles();
        refreshStartButton();
    } catch (err) {
        result.classList.remove('success');
        result.classList.add('error');
        result.textContent = err.message || 'Failed to reach endpoint';
        connectionValid = false;
        refreshStartButton();
    } finally {
        label.textContent = 'Test Connection';
    }
}

function serializeConfig() {
    const connection = getConnectionConfig();
    const metricStep = getSelectedMetricStepSeconds();
    return {
        endpoint: connection.url,
        tenant_id: connection.tenantId,
        auth_type: connection.authType,
        username: connection.authType === 'basic' ? connection.username : '',
        password: connection.authType === 'basic' ? connection.password : connection.token,
        custom_header_name: connection.authType === 'header' ? connection.headerName : '',
        custom_header_value: connection.authType === 'header' ? connection.headerValue : '',
        skip_tls_verify: connection.skipTls,
        metric_step_seconds: metricStep,
        batch_window_seconds: getBatchWindowSeconds(metricStep),
        drop_old: true,
        time_shift_ms: (Number(document.getElementById('timeShiftMinutes')?.value || 0) || 0) * 60 * 1000,
        max_labels_override: getManualMaxLabelsOverride(),
        drop_labels: getSelectedDropLabels(),
    };
}

function getBatchWindowSeconds(metricStep) {
    if (!metricStep || metricStep <= 30) return 30;
    if (metricStep <= 60) return 60;
    return 300;
}

function startImport() {
    const file = bundleInput.files[0];
    if (!connectionValid) {
        showStatus('Please test the connection first.', true);
        return;
    }
    if (!file) {
        showStatus('Select a bundle (.jsonl or .zip) to import.', true);
        return;
    }

    selectedBundleBytes = file.size;
    clearStatusActions();
    lockStartButton('Importing…');
    showStatus('Uploading bundle…', false);
    showImportProgressPanel();
    uploadBundle(file, serializeConfig());
}

function uploadBundle(file, config) {
    const form = new FormData();
    form.append('bundle', file);
    form.append('config', JSON.stringify(config));

    const xhr = new XMLHttpRequest();
    uploadStart = Date.now();
    xhr.open('POST', '/api/upload');
    uploadingBundle = true;

    xhr.upload.onprogress = event => {
        if (!event.lengthComputable) return;
        const percent = Math.round((event.loaded / event.total) * 100);
        updateImportProgress(percent, event.loaded, event.total);
    };

    xhr.onload = () => {
        uploadingBundle = false;
        let payload = null;
        try {
            payload = JSON.parse(xhr.responseText || '{}');
        } catch (err) {
            showStatus('Import finished but response was invalid JSON.', true);
            hideImportProgressPanel();
            return;
        }

        if (xhr.status >= 200 && xhr.status < 300 && payload.job_id) {
            loadRecentProfiles();
            showImportProgressPanel(false);
            startJobPolling(payload.job_id, payload.job || null);
        } else {
            loadRecentProfiles();
            showStatus(payload.error || 'Import failed', true);
            renderStatusActions(lastKnownJobSnapshot || { id: currentJobId }, true);
            hideImportProgressPanel();
            unlockStartButton();
        }
    };

    xhr.onerror = () => {
        showStatus('Network error while uploading bundle.', true);
        renderStatusActions(lastKnownJobSnapshot || { id: currentJobId }, true);
        loadRecentProfiles();
        hideImportProgressPanel();
        uploadingBundle = false;
        unlockStartButton();
    };

    xhr.send(form);
}

function clearAnalysisResult() {
    const analysisResult = document.getElementById('analysisResult');
    const analysisDetails = document.getElementById('analysisDetails');
    const rangeInfo = document.getElementById('fileRangeInfo');
    const shiftHint = document.getElementById('shiftHint');
    const picker = document.getElementById('targetStartPicker');
    const shiftSummary = document.getElementById('shiftSummary');
    const shiftNow = document.getElementById('shiftToNowBtn');
    const retentionInfo = document.getElementById('retentionInfo');
    lastAnalysisMode = 'sample';
    lastAnalysisSummary = null;
    lastMaxLabelsLimit = 0;
    lastMaxLabelsSource = 'unknown';
    lastLabelSimulation = null;
    if (analysisResult) {
        analysisResult.classList.remove('error');
        analysisResult.textContent = 'Preflight is optional but recommended: checks timestamps vs retention, validates JSONL, and estimates drops.';
    }
    if (analysisDetails) {
        analysisDetails.style.display = 'none';
        analysisDetails.innerHTML = '';
    }
    if (rangeInfo) {
        rangeInfo.style.display = 'none';
        rangeInfo.textContent = '';
    }
    if (shiftHint) {
        shiftHint.textContent = '';
    }
    if (picker) {
        picker.value = '';
    }
    if (shiftSummary) {
        shiftSummary.textContent = '';
    }
    if (retentionInfo) {
        retentionInfo.textContent = 'Fetched from target during preflight; shown in UTC.';
    }
    setSelectedDropLabels([]);
    renderLabelManager([], 0);
    clearMaxLabelsRisk();
    setShiftMinutes(0);
    if (shiftNow) {
        shiftNow.style.display = 'none';
    }
    setAnalysisLoader(false);
}

function renderAnalysisResult(data) {
    const analysisResult = document.getElementById('analysisResult');
    const analysisDetails = document.getElementById('analysisDetails');
    const shiftBtn = document.getElementById('applySuggestedShift');
    const rangeInfo = document.getElementById('fileRangeInfo');
    const picker = document.getElementById('targetStartPicker');
    const shiftHint = document.getElementById('shiftHint');
    const shiftNow = document.getElementById('shiftToNowBtn');
    const retentionInfo = document.getElementById('retentionInfo');
    if (!analysisResult || !analysisDetails) {
        return;
    }
    const summary = data.summary || {};
    lastAnalysisMode = String(data.analysis_mode || 'sample');
    lastAnalysisSummary = summary;
    lastMaxLabelsLimit = Number(data.max_labels_limit || summary.max_labels_limit || 0);
    lastMaxLabelsSource = String(data.max_labels_source || (lastMaxLabelsLimit > 0 ? 'metrics' : 'unknown'));
    lastLabelSimulation = buildLabelSimulation(summary);
    const warnings = data.warnings || [];
    protectedDropLabels = normalizeDropLabels(data.protected_labels || protectedDropLabels);
    if (!protectedDropLabels.length) {
        protectedDropLabels = ['__name__', 'job', 'instance'];
    }
    const maxLabelsLimit = lastMaxLabelsLimit;
    const rows = [];
    if (summary.start) {
        rows.push(`<div><strong>Time range:</strong> ${formatRFC(summary.start)} → ${formatRFC(summary.end || summary.start)}</div>`);
    }
    rows.push(`<div><strong>Points:</strong> ${summary.points || 0}, skipped: ${summary.skipped_lines || 0}, dropped old: ${summary.dropped_old || 0}</div>`);
    const scannedLines = Number(summary.scanned_lines || 0);
    const sampleLimit = Number(summary.sample_limit || 0);
    const sampleCut = Boolean(summary.sample_cut);
    if (lastAnalysisMode === 'full') {
        rows.push(`<div><strong>Preflight mode:</strong> full collection (${scannedLines} lines scanned).</div>`);
    } else {
        rows.push(`<div><strong>Preflight mode:</strong> sample (${scannedLines} lines scanned${sampleLimit > 0 ? `, limit=${sampleLimit}` : ''}${sampleCut ? ', truncated' : ''}).</div>`);
    }

    if (maxLabelsLimit > 0) {
        rows.push(`<div><strong>Label limit:</strong> target maxLabelsPerTimeseries=${maxLabelsLimit}${lastMaxLabelsSource === 'manual' ? ' (manual override)' : ''}; analyzed lines=${summary.analyzed_lines || 0}; over limit=${summary.over_label_limit || 0}; max seen=${summary.max_labels_seen || 0}</div>`);
    } else {
        rows.push(`<div><strong>Label limit:</strong> target maxLabelsPerTimeseries=unknown; analyzed lines=${summary.analyzed_lines || 0}; max seen=${summary.max_labels_seen || 0}</div>`);
    }
    const impact = evaluateDropImpact(lastLabelSimulation, getSelectedDropLabels(), maxLabelsLimit);
    if (impact && maxLabelsLimit > 0) {
        const mode = impact.overSeries > 0 ? 'risk' : 'ok';
        rows.push(`<div><strong>Drop impact (${mode}):</strong> over-limit sample series=${impact.overSeries}/${impact.seriesCount}; projected max labels=${impact.maxAfterDrop}; estimated sampled points at risk=${impact.overPoints}${impact.minAdditionalDrops > 0 ? `; at least ${impact.minAdditionalDrops} more label drop(s) needed on worst affected series` : ''}${impact.capped ? '; sample set capped' : ''}.</div>`);
    }
    if ((summary.over_label_limit || 0) > 0 || (impact && impact.overSeries > 0)) {
        const selectedDropCount = getSelectedDropLabels().length;
        rows.push(`<div><strong>Action:</strong> ${selectedDropCount > 0 ? `drop_labels currently selected: ${selectedDropCount}.` : 'select labels in “Label management” below to reduce label count before import.'}</div>`);
    }
    if ((summary.total_labels || 0) > 0) {
        const shownLabels = Array.isArray(summary.label_stats) ? summary.label_stats.length : 0;
        rows.push(`<div><strong>Labels:</strong> detected in sample=${summary.total_labels}; shown in manager=${shownLabels}</div>`);
    }
    if (summary.examples && summary.examples.length) {
        rows.push(`<div><strong>Example:</strong> <code>${formatSeriesExample(summary.examples[0])}</code></div>`);
    }
    if (warnings.length) {
        analysisResult.classList.add('error');
        rows.push(`<div><strong>Warnings:</strong> ${warnings.join(' ')}</div>`);
    } else {
        analysisResult.classList.remove('error');
    }
    analysisResult.textContent = warnings.length ? 'Preflight: issues detected.' : 'Preflight complete.';
    analysisDetails.innerHTML = rows.join('');
    analysisDetails.style.display = 'block';
    renderLabelManager(summary.label_stats || [], summary.total_labels || 0);
    updateMaxLabelsRisk(summary, maxLabelsLimit, lastMaxLabelsSource);
    const labelManager = document.getElementById('labelManagerDetails');
    if (labelManager && ((summary.over_label_limit || 0) > 0 || getSelectedDropLabels().length > 0)) {
        labelManager.open = true;
    }
    if (retentionInfo) {
        if (typeof data.retention_cutoff === 'number' && data.retention_cutoff > 0) {
            const cutoffDate = new Date(data.retention_cutoff);
            const approxWindowHours = Math.max(1, Math.round((Date.now() - data.retention_cutoff) / 3600000));
            const days = (approxWindowHours / 24).toFixed(1);
            retentionInfo.textContent = `Retention (from target): ~${days} days; cutoff ≈ ${cutoffDate.toISOString()} (UTC).${maxLabelsLimit > 0 ? ` maxLabelsPerTimeseries=${maxLabelsLimit}.` : ''}`;
        } else {
            retentionInfo.textContent = `Retention unknown (target did not return tsdb status).${maxLabelsLimit > 0 ? ` maxLabelsPerTimeseries=${maxLabelsLimit}.` : ''}`;
        }
    }
    if (shiftBtn) {
        const suggested = data.suggested_shift_ms || 0;
        if (suggested > 0) {
            shiftBtn.style.display = 'inline-flex';
            shiftBtn.dataset.shiftMs = String(suggested);
            shiftBtn.textContent = `Shift +${Math.round(suggested / 60000)} min`;
        } else {
            shiftBtn.style.display = 'none';
            shiftBtn.removeAttribute('data-shift-ms');
        }
    }
    if (rangeInfo) {
        if (summary.start) {
            rangeInfo.style.display = 'block';
            rangeInfo.textContent = `Detected: ${formatRFC(summary.start)} → ${formatRFC(summary.end || summary.start)}`;
        } else {
            rangeInfo.style.display = 'none';
            rangeInfo.textContent = '';
        }
    }
    if (picker && summary.start) {
        const startDate = new Date(summary.start);
        const endDate = summary.end ? new Date(summary.end) : startDate;
        const span = Math.max(0, endDate.getTime() - startDate.getTime());
        const maxStart = new Date(Date.now() - span);
        picker.value = formatDateTimeLocal(startDate);
        picker.max = formatDateTimeLocal(maxStart);
        picker.min = '';
        if (shiftNow) shiftNow.style.display = 'inline-flex';
        if (shiftHint) {
            shiftHint.style.color = 'var(--text-muted)';
            shiftHint.textContent = 'Adjust start to align bundle; end will not exceed now.';
        }
        setShiftMinutes(0);
    }
    updateShiftSummary();
    setAnalysisReady(true);
    refreshPreflightControls();
}

async function analyzeBundle(fullCollection = false) {
    const file = bundleInput.files[0];
    const analysisResult = document.getElementById('analysisResult');
    const fullBtn = document.getElementById('fullPreflightBtn');
    if (!analysisResult) return;
    if (!file) {
        analysisResult.textContent = 'Select a bundle file first.';
        analysisResult.classList.add('error');
        return;
    }
    analysisResult.classList.remove('error');
    analysisResult.innerHTML = '<span class="loading-spinner"></span> Validating bundle…';
    setAnalysisLoader(true);
    if (fullBtn) {
        fullBtn.disabled = true;
        fullBtn.textContent = fullCollection ? 'Running full collection…' : 'Full collection';
    }
    const details = document.getElementById('analysisDetails');
    if (details) {
        details.style.display = 'none';
        details.innerHTML = '';
    }
    lastAnalysisSummary = null;
    setAnalysisReady(false);

    const form = new FormData();
    form.append('bundle', file);
    form.append('config', JSON.stringify(serializeConfig()));
    if (fullCollection) {
        form.append('full_collection', '1');
    }

    try {
        analysisResult.textContent = fullCollection ? 'Running full collection analysis…' : 'Unpacking and analyzing…';
        const resp = await fetch('/api/analyze', { method: 'POST', body: form });
        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || 'Preflight failed');
        }
        renderAnalysisResult(data);
    } catch (err) {
        analysisResult.classList.add('error');
        analysisResult.textContent = err.message || 'Analysis failed';
        lastAnalysisSummary = null;
        lastLabelSimulation = null;
    } finally {
        loadRecentProfiles();
        setAnalysisLoader(false);
        refreshPreflightControls();
    }
}

function setShiftMinutes(minutes) {
    const input = document.getElementById('timeShiftMinutes');
    if (input) {
        input.value = minutes;
    }
    updateShiftSummary();
}

function updateShiftSummary() {
    const summaryEl = document.getElementById('shiftSummary');
    if (!summaryEl || !lastAnalysisSummary || !lastAnalysisSummary.start) {
        if (summaryEl) summaryEl.textContent = '';
        return;
    }
    const shiftMin = Number(document.getElementById('timeShiftMinutes')?.value || 0);
    const start = new Date(lastAnalysisSummary.start);
    const end = lastAnalysisSummary.end ? new Date(lastAnalysisSummary.end) : start;
    const shiftMs = shiftMin * 60000;
    const shiftedStart = new Date(start.getTime() + shiftMs);
    const shiftedEnd = new Date(end.getTime() + shiftMs);
    summaryEl.textContent = `Shift: ${shiftMin} min → ${formatRFC(shiftedStart)} to ${formatRFC(shiftedEnd)} (UTC)`;
}

function setAnalysisReady(ready) {
    analysisReady = ready;
    const picker = document.getElementById('targetStartPicker');
    const shiftNow = document.getElementById('shiftToNowBtn');
    const shiftBtn = document.getElementById('applySuggestedShift');
    if (picker) picker.disabled = !ready;
    if (shiftNow) shiftNow.disabled = !ready;
    if (shiftBtn) shiftBtn.disabled = !ready;
    refreshStartButton();
    refreshPreflightControls();
}

function refreshStartButton() {
    if (!startButton) return;
    const hasFile = bundleInput && bundleInput.files && bundleInput.files.length > 0;
    startButton.disabled = !(connectionValid && analysisReady && hasFile);
}

function setAnalysisLoader(show) {
    const loader = document.getElementById('analysisLoader');
    if (!loader) return;
    loader.style.display = show ? 'block' : 'none';
}

function refreshPreflightControls() {
    const fullBtn = document.getElementById('fullPreflightBtn');
    if (!fullBtn) return;
    const hasFile = bundleInput && bundleInput.files && bundleInput.files.length > 0;
    fullBtn.disabled = !hasFile || uploadingBundle;
    fullBtn.textContent = 'Full collection';
}

function applySuggestedShift() {
    const btn = document.getElementById('applySuggestedShift');
    if (!btn) return;
    const shiftMs = Number(btn.dataset.shiftMs || 0);
    const shiftMin = Math.round(shiftMs / 60000);
    setShiftMinutes(shiftMin);
    btn.style.display = 'none';
}

function handleTargetStartChange() {
    const picker = document.getElementById('targetStartPicker');
    const hint = document.getElementById('shiftHint');
    const shiftBtn = document.getElementById('applySuggestedShift');
    if (!picker || !lastAnalysisSummary || !lastAnalysisSummary.start) return;
    const start = new Date(lastAnalysisSummary.start);
    const end = lastAnalysisSummary.end ? new Date(lastAnalysisSummary.end) : start;
    const spanMs = Math.max(0, end.getTime() - start.getTime());
    const target = picker.value ? new Date(picker.value) : null;
    if (!target || isNaN(target.getTime())) return;
    const maxStart = new Date(Date.now() - spanMs);
    if (target.getTime() > maxStart.getTime()) {
        picker.value = formatDateTimeLocal(maxStart);
        if (hint) {
            hint.style.color = 'var(--color-error)';
            hint.textContent = 'Adjusted: end cannot be in the future.';
        }
    }
    const startMs = start.getTime();
    const shiftMs = new Date(picker.value).getTime() - startMs;
    setShiftMinutes(Math.round(shiftMs / 60000));
    if (hint) {
        hint.style.color = 'var(--text-muted)';
        hint.textContent = `Shift set to ${Math.round(shiftMs / 60000)} min to align start.`;
    }
    if (shiftBtn) {
        shiftBtn.style.display = 'none';
    }
}

function shiftBundleToNow() {
    if (!lastAnalysisSummary || !lastAnalysisSummary.start) return;
    const start = new Date(lastAnalysisSummary.start);
    const end = lastAnalysisSummary.end ? new Date(lastAnalysisSummary.end) : start;
    const spanMs = Math.max(0, end.getTime() - start.getTime());
    const targetStartMs = Date.now() - spanMs;
    const picker = document.getElementById('targetStartPicker');
    if (picker) {
        picker.value = formatDateTimeLocal(new Date(targetStartMs));
    }
    const shiftMs = targetStartMs - start.getTime();
    setShiftMinutes(Math.round(shiftMs / 60000));
    const hint = document.getElementById('shiftHint');
    if (hint) {
        hint.style.color = 'var(--text-muted)';
        hint.textContent = 'Shifted so the last sample lands at now.';
    }
    const shiftBtn = document.getElementById('applySuggestedShift');
    if (shiftBtn) shiftBtn.style.display = 'none';
}

function renderImportResult(payload) {
    stopJobPolling();
    hideImportProgressPanel();
    const summary = payload.import_summary || payload.summary || {};
    const verification = payload.verification || null;
    if (!statusPanel) return;

    let html = `<div><strong>Import complete:</strong> ${formatBytes(payload.bytes_sent || summary.bytes || 0)} sent to ${payload.remote_path || summary.remote_path || 'target endpoint'}.</div>`;
    html += `<div>${formatVolumeText(summary.source_bytes, summary.inflated_bytes, summary.chunks)}</div>`;

    if (summary.start) {
        html += `<div><strong>Time range:</strong> ${formatRFC(summary.start)} → ${formatRFC(summary.end || summary.start)}</div>`;
    }

    if (Array.isArray(summary.examples) && summary.examples.length) {
        const items = summary.examples.slice(0, 5).map(ex => `<li><code>${formatSeriesExample(ex)}</code></li>`).join('');
        html += `<div><strong>Example series:</strong><ul>${items}</ul></div>`;
    }

    if (verification) {
        html += `<div><strong>Verification:</strong> ${verification.message || 'Query executed.'}</div>`;
        if (verification.query) {
            html += `<pre>${verification.query}</pre>`;
        }
    }
    if ((summary.over_label_limit || 0) > 0 && (summary.max_labels_limit || 0) > 0) {
        html += `<div><strong>Warning:</strong> ${summary.over_label_limit} series were sent with labels above maxLabelsPerTimeseries=${summary.max_labels_limit} (max seen=${summary.max_labels_seen || 'n/a'}). Target may drop them.</div>`;
    }

    showStatus(html, false, true, true);
}

function showImportProgressPanel(resetValues = true) {
    const panel = document.getElementById('importProgressPanel');
    if (!panel) return;
    panel.classList.remove('hidden');
    panel.style.display = 'block';
    if (resetValues) {
        document.getElementById('importProgressPercent').textContent = '0%';
        document.getElementById('importProgressFill').style.width = '0%';
        document.getElementById('importProgressEta').textContent = 'Uploading bundle…';
        document.getElementById('importStage').textContent = 'Uploading bundle…';
        document.getElementById('importProgressMetrics').textContent = 'Preparing upload…';
        document.getElementById('importProgressSummary').textContent = `${formatBytes(selectedBundleBytes)} compressed bundle (inflated size pending…)`;
        document.getElementById('importBatchWindow').textContent = `Chunk size: ${formatBytes(JOB_CHUNK_SIZE_HINT)}`;
        document.getElementById('importProgressBatches').textContent = '0 / 0 chunks';
        updateProgressDebugDetail('');
    }
}

function hideImportProgressPanel() {
    const panel = document.getElementById('importProgressPanel');
    if (panel) {
        panel.classList.add('hidden');
        panel.style.display = 'none';
    }
    stopJobPolling();
}

function updateImportProgress(percent, loadedBytes, totalBytes) {
    const fill = document.getElementById('importProgressFill');
    if (fill) {
        fill.style.width = `${percent}%`;
    }
    const percentLabel = document.getElementById('importProgressPercent');
    if (percentLabel) {
        percentLabel.textContent = `${percent}%`;
    }

    const etaEl = document.getElementById('importProgressEta');
    if (etaEl) {
        const elapsed = (Date.now() - uploadStart) / 1000;
        etaEl.textContent = percent < 100 ? `Uploading bundle… ${formatBytes(loadedBytes)} / ${formatBytes(totalBytes)}` : `Uploaded in ${Math.round(elapsed)}s`;
    }

    const stageEl = document.getElementById('importStage');
    if (stageEl) {
        stageEl.textContent = 'Uploading bundle…';
    }

    const metricsLabel = document.getElementById('importProgressMetrics');
    if (metricsLabel) {
        metricsLabel.textContent = `${formatBytes(loadedBytes)} / ${formatBytes(totalBytes)}`;
    }

    const summaryEl = document.getElementById('importProgressSummary');
    if (summaryEl) {
        summaryEl.textContent = `${formatBytes(selectedBundleBytes)} compressed bundle`;
    }
}

function formatLabelList(labels = {}) {
    const keys = Object.keys(labels);
    if (!keys.length) return '';
    const readable = keys.slice(0, 4).map(key => `${key}="${labels[key]}"`).join(', ');
    return `(${readable}${keys.length > 4 ? ', …' : ''})`;
}

function formatDateTimeLocal(date) {
    if (!date) return '';
    const tzOffset = date.getTimezoneOffset() * 60000;
    const localISO = new Date(date.getTime() - tzOffset).toISOString().slice(0, 16);
    return localISO;
}

function formatRFC(value) {
    if (!value) return 'n/a';
    try {
        return new Date(value).toISOString();
    } catch {
        return value;
    }
}

function showStatus(text, isError = false, success = false, asHTML = false) {
    if (!statusPanel) return;
    statusPanel.style.display = 'block';
    if (asHTML) {
        statusPanel.innerHTML = text;
    } else {
        statusPanel.textContent = text;
    }
    statusPanel.classList.remove('error', 'success');
    if (isError) {
        statusPanel.classList.add('error');
    } else if (success) {
        statusPanel.classList.add('success');
    }
}

function updateProgressDebugDetail(text) {
    const debug = document.getElementById('importProgressEtaDetail');
    if (!debug) return;
    debug.textContent = text || '';
}

function formatJobSnapshot(job) {
    if (!job) return '';
    const updated = job.updated_at ? new Date(job.updated_at).toLocaleTimeString() : 'n/a';
    const chunks = job.chunks_total
        ? `${job.chunks_completed || 0}/${job.chunks_total}`
        : `${job.chunks_completed || 0}`;
    return `Job ${job.id || 'unknown'} · state=${job.state || '-'} · stage=${job.stage || '-'} · chunks=${chunks} · updated=${updated}`;
}

function formatBytes(bytes) {
    if (!bytes && bytes !== 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB'];
    let idx = 0;
    let value = bytes;
    while (value >= 1024 && idx < units.length - 1) {
        value /= 1024;
        idx++;
    }
    return `${value.toFixed(1)} ${units[idx]}`;
}

// Metric step reuse from vmgather
function initializeMetricStepSelector() {
    const select = document.getElementById('metricStep');
    if (select) {
        select.addEventListener('change', () => applyRecommendedMetricStep(false));
    }
    applyRecommendedMetricStep(true);
}

function applyRecommendedMetricStep(forceApply) {
    const select = document.getElementById('metricStep');
    const hint = document.getElementById('metricStepHint');
    if (!select) return;

    let recommended = 60;
    const file = bundleInput?.files?.[0];
    if (file) {
        const sizeMB = file.size / (1024 * 1024);
        if (sizeMB < 50) {
            recommended = 30;
        } else if (sizeMB < 200) {
            recommended = 60;
        } else {
            recommended = 300;
        }
    }

    const selectedSeconds = Number(select.value || 0);
    if (hint) {
        if (selectedSeconds > 0) {
            const recommendation = formatStepLabel(recommended);
            const current = formatStepLabel(selectedSeconds);
            hint.textContent = `Current step: ${current}. Recommended: ${recommendation} based on bundle size.`;
        } else {
            hint.textContent = `Current bundle suggests ${formatStepLabel(recommended)} batches.`;
        }
    }

    if (forceApply && (!select.value || select.value === 'auto')) {
        select.value = String(recommended);
    }
}

function formatVolumeText(compressed, inflated, chunks) {
    if (!compressed && !inflated) {
        return '';
    }
    if (!inflated) {
        return `Volume: ${formatBytes(compressed)} bundle`;
    }
    const chunkInfo = chunks ? ` via ${chunks} chunk(s)` : '';
    return `Volume: ${formatBytes(compressed)} compressed → ${formatBytes(inflated)} JSONL${chunkInfo}`;
}

function formatSeriesExample(example = {}) {
    const name = example.__name__ || example.metric_name || 'series';
    const labels = Object.entries(example)
        .filter(([key]) => key !== '__name__' && key !== 'metric_name')
        .map(([key, value]) => `${key}="${value}"`);
    return `${name}{${labels.join(', ')}}`;
}

function startJobPolling(jobId, snapshot) {
    stopJobPolling();
    currentJobId = jobId;
    lastKnownJobId = jobId;
    jobPollErrors = 0;
    lastJobUpdateAtMs = Date.now();
    lastJobUpdatedToken = '';
    stallWarningShown = false;
    updateProgressDebugDetail('');
    if (snapshot) {
        lastKnownJobSnapshot = snapshot;
        updateJobProgressUI(snapshot);
        if (snapshot.updated_at) {
            lastJobUpdatedToken = snapshot.updated_at;
        }
    }
    const poll = async () => {
        if (!currentJobId) return;
        try {
            const controller = new AbortController();
            const timeout = setTimeout(() => controller.abort(), JOB_POLL_REQUEST_TIMEOUT_MS);
            let resp;
            try {
                resp = await fetch(`/api/import/status?id=${encodeURIComponent(jobId)}`, {
                    signal: controller.signal,
                    cache: 'no-store',
                });
            } finally {
                clearTimeout(timeout);
            }
            if (!resp.ok) {
                throw new Error(`status polling returned HTTP ${resp.status}`);
            }
            const job = await resp.json();
            lastKnownJobSnapshot = job;
            jobPollErrors = 0;
            if (job.updated_at && job.updated_at !== lastJobUpdatedToken) {
                lastJobUpdatedToken = job.updated_at;
                lastJobUpdateAtMs = Date.now();
                stallWarningShown = false;
            }
            updateJobProgressUI(job);
            if (job.state === 'completed') {
                renderImportResult({
                    import_summary: job.summary || {},
                    verification: job.verification || null,
                    bytes_sent: job.summary?.bytes,
                    remote_path: job.remote_path,
                });
                stopJobPolling();
                unlockStartButton();
            } else if (job.state === 'failed') {
                const chunkText = job.chunks_total
                    ? ` (${job.chunks_completed || 0}/${job.chunks_total} chunks)`
                    : '';
                const message = job.resume_ready
                    ? `${job.error || 'Import failed'}${chunkText}. You can resume from the saved offset.`
                    : (job.error || 'Import failed');
                showStatus(message, true);
                renderStatusActions(job, true);
                stopJobPolling();
                unlockStartButton();
            } else if (job.state === 'running') {
                const stalledForMs = Date.now() - lastJobUpdateAtMs;
                if (stalledForMs > JOB_PROGRESS_STALL_MS && !stallWarningShown) {
                    const stalledSec = Math.round(stalledForMs / 1000);
                    showStatus(`Import appears stalled: no progress update for ${stalledSec}s. Use Retry Status to re-check job state or Resume if backend marked it resumable.`, true);
                    renderStatusActions(job, true);
                    stallWarningShown = true;
                }
            }
        } catch (err) {
            console.error('Job polling failed', err);
            jobPollErrors += 1;
            const errorMessage = err && err.name === 'AbortError'
                ? `status polling timeout after ${Math.round(JOB_POLL_REQUEST_TIMEOUT_MS / 1000)}s`
                : (err?.message || 'unknown polling error');
            updateProgressDebugDetail(`Polling errors: ${jobPollErrors}/${JOB_POLL_MAX_ERRORS}. Last error: ${errorMessage}`);
            if (jobPollErrors >= JOB_POLL_MAX_ERRORS) {
                const snapshotHint = formatJobSnapshot(lastKnownJobSnapshot);
                const suffix = snapshotHint ? ` Last known: ${snapshotHint}.` : '';
                showStatus(`Lost connection to import job after ${JOB_POLL_MAX_ERRORS} polling errors (${errorMessage}). Retry Status to continue monitoring.${suffix}`, true);
                renderStatusActions(lastKnownJobSnapshot || { id: jobId }, true);
                stopJobPolling();
                unlockStartButton();
            }
        }
    };
    poll();
    jobPollTimer = setInterval(poll, JOB_POLL_INTERVAL_MS);
}

function stopJobPolling() {
    if (jobPollTimer) {
        clearInterval(jobPollTimer);
        jobPollTimer = null;
    }
    currentJobId = null;
    jobPollErrors = 0;
    lastJobUpdateAtMs = 0;
    lastJobUpdatedToken = '';
}

function updateJobProgressUI(job) {
    if (!job || uploadingBundle) return;
    lastKnownJobSnapshot = job;
    const percent = Math.max(0, Math.min(100, Math.round(job.percent || 0)));
    const percentLabel = document.getElementById('importProgressPercent');
    if (percentLabel) percentLabel.textContent = `${percent}%`;
    const fill = document.getElementById('importProgressFill');
    if (fill) fill.style.width = `${percent}%`;

    const stageEl = document.getElementById('importStage');
    if (stageEl) stageEl.textContent = formatStage(job.stage);
    const etaEl = document.getElementById('importProgressEta');
    if (etaEl) etaEl.textContent = job.message || '';
    const summaryEl = document.getElementById('importProgressSummary');
    if (summaryEl) summaryEl.textContent = formatVolumeText(job.source_bytes, job.inflated_bytes, job.chunks_completed || job.chunks_total);
    const batchEl = document.getElementById('importProgressBatches');
    if (batchEl) {
        if (job.chunks_total) {
            batchEl.textContent = `${job.chunks_completed || 0} / ${job.chunks_total} chunks`;
        } else {
            batchEl.textContent = `${job.chunks_completed || 0} chunks`;
        }
    }
    const chunkSizeEl = document.getElementById('importBatchWindow');
    if (chunkSizeEl && job.chunk_size) {
        chunkSizeEl.textContent = `Chunk size: ${formatBytes(job.chunk_size)}`;
    }
    const metricsEl = document.getElementById('importProgressMetrics');
    if (metricsEl) {
        metricsEl.textContent = job.remote_path || 'Streaming chunks…';
    }

    updateProgressDebugDetail(formatJobSnapshot(job));
    renderStatusActions(job, false);
}

function formatStage(stage) {
    switch ((stage || '').toLowerCase()) {
        case 'extracting':
            return 'Extracting bundle…';
        case 'importing':
            return 'Streaming JSONL chunks…';
        case 'verifying':
            return 'Verifying imported data…';
        case 'completed':
            return 'Done';
        case 'failed':
            return 'Failed';
        default:
            return stage || 'Processing…';
    }
}

function lockStartButton(label) {
    if (!startButton) return;
    startButton.disabled = true;
    startButton.textContent = label || 'Importing…';
}

function unlockStartButton() {
    if (!startButton) return;
    startButton.disabled = false;
    startButton.textContent = 'Start Import';
}

function renderStatusActions(job, showRetryStatus = false) {
    if (!statusPanel) return;
    clearStatusActions();
    const actions = document.createElement('div');
    actions.id = 'statusActionButtons';
    actions.style.display = 'flex';
    actions.style.gap = '8px';
    actions.style.marginTop = '10px';
    actions.style.flexWrap = 'wrap';

    if (job && job.state === 'failed' && job.resume_ready && job.id) {
        const resumeBtn = document.createElement('button');
        resumeBtn.textContent = 'Resume Import';
        resumeBtn.className = 'btn-primary';
        resumeBtn.onclick = () => resumeImport(job.id);
        resumeBtn.id = 'resumeImportBtn';
        actions.appendChild(resumeBtn);
    }

    const retryJobID = (job && job.id) || lastKnownJobId;
    if (showRetryStatus && retryJobID) {
        const retryBtn = document.createElement('button');
        retryBtn.textContent = 'Retry Status';
        retryBtn.className = 'btn-secondary';
        retryBtn.id = 'retryStatusBtn';
        retryBtn.onclick = () => retryStatusPolling(retryJobID);
        actions.appendChild(retryBtn);
    }

    if (actions.childElementCount > 0) {
        statusPanel.appendChild(actions);
    }
}

function clearStatusActions() {
    const actions = document.getElementById('statusActionButtons');
    if (actions && actions.parentNode) {
        actions.parentNode.removeChild(actions);
    }
    const retryBtn = document.getElementById('retryStatusBtn');
    if (retryBtn && retryBtn.parentNode) {
        retryBtn.parentNode.removeChild(retryBtn);
    }
    const existing = document.getElementById('resumeImportBtn');
    if (existing && existing.parentNode) {
        existing.parentNode.removeChild(existing);
    }
}

function renderResumeCTA(job) {
    renderStatusActions(job, false);
}

function clearResumeCTA() {
    clearStatusActions();
}

function retryStatusPolling(jobId) {
    const targetJobID = jobId || lastKnownJobId;
    if (!targetJobID) {
        showStatus('No import job to monitor. Start a new import.', true);
        return;
    }
    const snapshot = lastKnownJobSnapshot && lastKnownJobSnapshot.id === targetJobID
        ? lastKnownJobSnapshot
        : null;
    showStatus(`Retrying status polling for ${targetJobID}…`, false);
    clearStatusActions();
    showImportProgressPanel(false);
    lockStartButton('Importing…');
    startJobPolling(targetJobID, snapshot);
}

async function resumeImport(jobId) {
    if (!jobId) return;
    try {
        showStatus('Resuming import…', false, false);
        clearStatusActions();
        const resp = await fetch(`/api/import/resume?id=${encodeURIComponent(jobId)}`, { method: 'POST' });
        if (!resp.ok) {
            const body = await resp.text();
            showStatus(body || 'Resume failed', true);
            renderStatusActions({ state: 'failed', resume_ready: true, id: jobId }, true);
            return;
        }
        showImportProgressPanel(false);
        startJobPolling(jobId, lastKnownJobSnapshot && lastKnownJobSnapshot.id === jobId ? lastKnownJobSnapshot : null);
        lockStartButton('Importing…');
    } catch (err) {
        showStatus(`Resume failed: ${err.message}`, true);
        renderStatusActions({ state: 'failed', resume_ready: true, id: jobId }, true);
    }
}

function getSelectedMetricStepSeconds() {
    const select = document.getElementById('metricStep');
    if (!select) return 60;
    const value = select.value;
    if (!value || value === 'auto') {
        return 60;
    }
    const parsed = parseInt(value, 10);
    return Number.isNaN(parsed) ? 60 : parsed;
}

function formatStepLabel(seconds) {
    if (!seconds || seconds < 60) return `${seconds}s`;
    const minutes = seconds / 60;
    if (Number.isInteger(minutes)) return `${minutes} min`;
    return `${seconds}s`;
}

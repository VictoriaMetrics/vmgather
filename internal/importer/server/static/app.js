const JOB_CHUNK_SIZE_HINT = 512 * 1024;
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
let uploadingBundle = false;
let lastAnalysisSummary = null;

document.addEventListener('DOMContentLoaded', () => {
    toggleAuthFields();
    toggleTenantInput();
    initializeUrlValidation();
    initializeMetricStepSelector();
    initDropZone();
    refreshStartButton();
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
    totalBatches = 0;
    setAnalysisReady(false);
    refreshStartButton();
    clearResumeCTA();
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
            startJobPolling(payload.job_id, payload.job || null);
        } else {
            showStatus(payload.error || 'Import failed', true);
            hideImportProgressPanel();
            unlockStartButton();
        }
    };

    xhr.onerror = () => {
        showStatus('Network error while uploading bundle.', true);
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
    lastAnalysisSummary = null;
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
    lastAnalysisSummary = summary;
    const warnings = data.warnings || [];
    const rows = [];
    if (summary.start) {
        rows.push(`<div><strong>Time range:</strong> ${formatRFC(summary.start)} → ${formatRFC(summary.end || summary.start)}</div>`);
    }
    rows.push(`<div><strong>Points:</strong> ${summary.points || 0}, skipped: ${summary.skipped_lines || 0}, dropped old: ${summary.dropped_old || 0}</div>`);
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
    if (retentionInfo) {
        if (typeof data.retention_cutoff === 'number' && data.retention_cutoff > 0) {
            const cutoffDate = new Date(data.retention_cutoff);
            const approxWindowHours = Math.max(1, Math.round((Date.now() - data.retention_cutoff) / 3600000));
            const days = (approxWindowHours / 24).toFixed(1);
            retentionInfo.textContent = `Retention (from target): ~${days} days; cutoff ≈ ${cutoffDate.toISOString()} (UTC).`;
        } else {
            retentionInfo.textContent = 'Retention unknown (target did not return tsdb status).';
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
}

async function analyzeBundle() {
    const file = bundleInput.files[0];
    const analysisResult = document.getElementById('analysisResult');
    if (!analysisResult) return;
    if (!file) {
        analysisResult.textContent = 'Select a bundle file first.';
        analysisResult.classList.add('error');
        return;
    }
    analysisResult.classList.remove('error');
    analysisResult.innerHTML = '<span class="loading-spinner"></span> Validating bundle…';
    setAnalysisLoader(true);
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

    try {
        analysisResult.textContent = 'Unpacking and analyzing…';
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
    } finally {
        setAnalysisLoader(false);
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

    showStatus(html, false, true, true);
}

function showImportProgressPanel() {
    const panel = document.getElementById('importProgressPanel');
    if (!panel) return;
    panel.classList.remove('hidden');
    panel.style.display = 'block';

    document.getElementById('importProgressPercent').textContent = '0%';
    document.getElementById('importProgressFill').style.width = '0%';
    document.getElementById('importProgressEta').textContent = 'Uploading bundle…';
    document.getElementById('importStage').textContent = 'Uploading bundle…';
    document.getElementById('importProgressMetrics').textContent = 'Preparing upload…';
    document.getElementById('importProgressSummary').textContent = `${formatBytes(selectedBundleBytes)} compressed bundle (inflated size pending…)`;
    document.getElementById('importBatchWindow').textContent = `Chunk size: ${formatBytes(JOB_CHUNK_SIZE_HINT)}`;
    document.getElementById('importProgressBatches').textContent = '0 / 0 chunks';
    document.getElementById('importProgressEtaDetail').textContent = '';
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

// Metric step reuse from VMExporter
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
    if (snapshot) {
        updateJobProgressUI(snapshot);
    }
    const poll = async () => {
        if (!currentJobId) return;
        try {
            const resp = await fetch(`/api/import/status?id=${encodeURIComponent(jobId)}`);
            if (!resp.ok) return;
            const job = await resp.json();
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
                showStatus(job.error || 'Import failed', true);
                stopJobPolling();
                hideImportProgressPanel();
                unlockStartButton();
            }
        } catch (err) {
            console.error('Job polling failed', err);
        }
    };
    poll();
    jobPollTimer = setInterval(poll, 1200);
}

function stopJobPolling() {
    if (jobPollTimer) {
        clearInterval(jobPollTimer);
        jobPollTimer = null;
    }
    currentJobId = null;
}

function updateJobProgressUI(job) {
    if (!job || uploadingBundle) return;
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

    renderResumeCTA(job);
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

function renderResumeCTA(job) {
    if (!statusPanel) return;
    clearResumeCTA();
    if (job && job.state === 'failed' && job.resume_ready && job.id) {
        const btn = document.createElement('button');
        btn.textContent = 'Resume Import';
        btn.className = 'btn-primary';
        btn.style.marginLeft = '10px';
        btn.onclick = () => resumeImport(job.id);
        btn.id = 'resumeImportBtn';
        statusPanel.appendChild(btn);
    }
}

function clearResumeCTA() {
    const existing = document.getElementById('resumeImportBtn');
    if (existing && existing.parentNode) {
        existing.parentNode.removeChild(existing);
    }
}

async function resumeImport(jobId) {
    if (!jobId) return;
    try {
        showStatus('Resuming import…', false, false);
        const resp = await fetch(`/api/import/resume?id=${encodeURIComponent(jobId)}`, { method: 'POST' });
        if (!resp.ok) {
            const body = await resp.text();
            showStatus(body || 'Resume failed', true);
            return;
        }
        startJobPolling(jobId);
        showImportProgressPanel();
    } catch (err) {
        showStatus(`Resume failed: ${err.message}`, true);
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

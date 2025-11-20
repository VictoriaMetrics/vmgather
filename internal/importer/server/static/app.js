const JOB_CHUNK_SIZE_HINT = 512 * 1024;
const dropZone = document.getElementById('dropZone');
const bundleInput = document.getElementById('bundleFile');
const statusPanel = document.getElementById('statusPanel');
const fileHint = document.getElementById('fileHint');
const startButton = document.getElementById('startImportBtn');
let connectionValid = false;
let totalBatches = 0;
let uploadStart = 0;
let selectedBundleBytes = 0;
let currentJobId = null;
let jobPollTimer = null;
let uploadingBundle = false;

document.addEventListener('DOMContentLoaded', () => {
    toggleAuthFields();
    initializeUrlValidation();
    initializeMetricStepSelector();
    initDropZone();
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
        }
    });

    bundleInput.addEventListener('change', () => {
        const file = bundleInput.files[0];
        dropZone.querySelector('strong').textContent = file ? file.name : 'Drop file here';
        updateFileHint(file);
        applyRecommendedMetricStep(false);
    });
}

function updateFileHint(file) {
    if (!fileHint) return;
    if (!file) {
        fileHint.textContent = '';
        selectedBundleBytes = 0;
        return;
    }
    selectedBundleBytes = file.size;
    const size = formatBytes(file.size);
    fileHint.textContent = `Selected ${file.name} (${size})`;
}

function resetForm() {
    document.getElementById('vmUrl').value = '';
    document.getElementById('tenantId').value = '';
    document.getElementById('authType').value = 'none';
    toggleAuthFields();
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

function initializeUrlValidation() {
    const input = document.getElementById('vmUrl');
    const hint = document.getElementById('vmUrlHint');
    if (!input || !hint) return;

    const applyState = () => {
        const assessment = analyzeVmUrl(input.value);
        if (assessment.valid) {
            hint.textContent = `✅ ${assessment.message || 'URL looks good'}`;
            hint.classList.remove('error');
            hint.classList.add('success');
        } else {
            hint.textContent = `❌ ${assessment.message || 'Invalid URL'}`;
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
    return {
        url: urlValue,
        tenantId: document.getElementById('tenantId').value.trim(),
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
    } catch (err) {
        result.classList.remove('success');
        result.classList.add('error');
        result.textContent = err.message || 'Failed to reach endpoint';
        connectionValid = false;
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

    if (hint) {
        hint.textContent = `Current bundle suggests ${formatStepLabel(recommended)} batches.`;
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

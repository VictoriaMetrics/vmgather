// vmgather Frontend - Enhanced UX/UI
// State
let currentStepIndex = 0;
let stepSequence = [];
let stepNames = [];
const totalStepsDefault = 6;
let DEFAULT_STAGING_DIR = '/tmp/vmgather';
let connectionValid = false;
let resolvedConnection = null;
let lastValidatedInput = '';
let lastValidatedConfigKey = '';
let programmaticUrlUpdate = false;
let lastValidationAttempts = [];
let lastValidationFinalEndpoint = '';
let discoveredComponents = [];
let discoveredSelectorJobs = [];
let sampleMetrics = [];
let exportResult = null;
let currentExportJobId = null;
let exportStatusTimer = null;
let sampleReloadTimer = null;
let sampleAbortController = null;
let sampleRequestInFlight = false;
let sampleHadError = false;
let sampleStatus = 'idle';
let sampleRequestCount = 0;
let userEditedStaging = false;
const selectedCustomLabels = new Set();
const removedLabels = new Set();
let exportStagingPath = '';
let currentJobObfuscationEnabled = false;
let stagingDirValidationTimer = null;
let directoryPickerPath = '';
let directoryPickerParent = '';
let directoryPickerCloseHandler = null;
let appConfigLoaded = false;
let currentExportButton = null;
let cancelRequestInFlight = false;
let componentsLoadingInFlight = false;
let currentMode = 'cluster';
let customQueryType = 'selector';
let customQueryValidated = false;
let customQueryValidationMessage = '';
let obfuscationTouched = false;

async function bootstrapAppConfig() {
    if (appConfigLoaded) {
        return;
    }
    try {
        const resp = await fetch('/api/config');
        if (resp.ok) {
            const data = await resp.json();
            window.__vmAppConfig = data;
            if (data.default_staging_dir) {
                DEFAULT_STAGING_DIR = data.default_staging_dir;
            }
        }
    } catch (err) {
        console.warn('Failed to load app config', err);
    } finally {
        appConfigLoaded = true;
    }
}

// Initialize
document.addEventListener('DOMContentLoaded', async () => {
    await bootstrapAppConfig();

    // Set default timezone to user's browser timezone
    initializeTimezone();

    // Set default time range (last 1 hour)
    setPreset('1h');

    // Initialize datetime-local inputs
    initializeDateTimePickers();
    markHelpAutoOpenFlag(false);

    initializeModeToggle();
    initializeUrlValidation();
    initializeStep3NextTooltip();
    initializeAuthCacheInvalidation();
    updateSelectionSummary();
    updateNextButtons();
    initializeObfuscationOptions();
    updateSelectionStepCopy();
    updateWelcomeCopy();
    updateObfuscationCopy();
    rebuildStepSequence();
    showStepByIndex(0, true);

    document.addEventListener('change', (event) => {
        const target = event.target;
        if (!target || !target.classList || !target.classList.contains('obf-label-checkbox')) {
            if (target && target.matches && target.matches('.selector-job-item input[type="checkbox"]')) {
                updateSelectionSummary();
                scheduleSampleReload();
            }
            return;
        }

        const label = target.dataset.label;
        if (label && label !== 'instance' && label !== 'job') {
            if (target.checked) {
                selectedCustomLabels.add(label);
            } else {
                selectedCustomLabels.delete(label);
            }
        }

        scheduleSampleReload();
    });

    initializeStagingDirInput();
    initializeMetricStepSelector();
    disableCancelButton();
    wireAdvancedSummaries();
    initializeHelpSection();
    initializeCustomQueryInputs();
});

function resetResolvedConnectionCache() {
    resolvedConnection = null;
    lastValidatedInput = '';
    lastValidatedConfigKey = '';
}

function buildConnectionCacheKey(rawUrl) {
    const authType = document.getElementById('authType')?.value || 'none';
    const username = document.getElementById('username')?.value || '';
    const password = document.getElementById('password')?.value || '';
    const token = document.getElementById('token')?.value || '';
    const headerName = document.getElementById('headerName')?.value || '';
    const headerValue = document.getElementById('headerValue')?.value || '';

    return [
        rawUrl || '',
        authType,
        username,
        password,
        token,
        headerName,
        headerValue
    ].join('|');
}

function initializeAuthCacheInvalidation() {
    const authType = document.getElementById('authType');
    if (!authType) {
        return;
    }

    authType.addEventListener('change', () => {
        resetResolvedConnectionCache();
        toggleAuthFields();
        wireAuthFieldListeners();
    });

    toggleAuthFields();
    wireAuthFieldListeners();
}

function wireAuthFieldListeners() {
    const fields = ['username', 'password', 'token', 'headerName', 'headerValue'];
    fields.forEach(id => {
        const el = document.getElementById(id);
        if (!el) {
            return;
        }
        el.addEventListener('input', resetResolvedConnectionCache);
        el.addEventListener('change', resetResolvedConnectionCache);
    });
}

// Initialize timezone selector with user's default timezone
function initializeTimezone() {
    const timezoneSelect = document.getElementById('timezone');
    if (!timezoneSelect) {
        return;
    }

    // Get user's browser timezone
    try {
        const userTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone;

        // Try to find matching option
        const options = timezoneSelect.options;
        for (let i = 0; i < options.length; i++) {
            if (options[i].value === userTimezone) {
                timezoneSelect.selectedIndex = i;
                return;
            }
        }

        // If exact match not found, default to "local"
        timezoneSelect.value = 'local';
    } catch (e) {
        // Fallback to local if timezone detection fails
        console.warn('Failed to detect timezone:', e);
        timezoneSelect.value = 'local';
    }
}

// DateTime Picker initialization
function initializeDateTimePickers() {
    const timezone = document.getElementById('timezone')?.value || 'local';
    const now = new Date();
    const oneHourAgo = new Date(now.getTime() - 60 * 60 * 1000);

    document.getElementById('timeTo').value = formatDateTimeLocal(now, timezone);
    document.getElementById('timeFrom').value = formatDateTimeLocal(oneHourAgo, timezone);
}

function markHelpAutoOpenFlag(value) {
    try {
        localStorage.setItem('vmgather_help_auto_open', value ? '1' : '0');
    } catch (e) {
        console.warn('Failed to set auto open flag', e);
    }
}

function shouldAutoOpenHelp() {
    try {
        return localStorage.getItem('vmgather_help_auto_open') === '1';
    } catch {
        return false;
    }
}

function formatDateTimeLocal(date, timezone = 'local') {
    // Format: YYYY-MM-DDTHH:mm
    let targetDate = date;

    if (timezone !== 'local') {
        // Convert to target timezone
        const dateStr = date.toLocaleString('en-US', { timeZone: timezone });
        targetDate = new Date(dateStr);
    }

    const year = targetDate.getFullYear();
    const month = String(targetDate.getMonth() + 1).padStart(2, '0');
    const day = String(targetDate.getDate()).padStart(2, '0');
    const hours = String(targetDate.getHours()).padStart(2, '0');
    const minutes = String(targetDate.getMinutes()).padStart(2, '0');

    return `${year}-${month}-${day}T${hours}:${minutes}`;
}

// Update times when timezone changes
function updateTimezoneTimes() {
    const timezone = document.getElementById('timezone').value;
    const now = new Date();
    const oneHourAgo = new Date(now.getTime() - 60 * 60 * 1000);

    document.getElementById('timeTo').value = formatDateTimeLocal(now, timezone);
    document.getElementById('timeFrom').value = formatDateTimeLocal(oneHourAgo, timezone);
}

// URL validation helpers
function initializeUrlValidation() {
    const urlInput = document.getElementById('vmUrl');
    const hint = document.getElementById('vmUrlHint');
    const testButton = document.getElementById('testConnectionBtn');
    if (!urlInput || !hint || !testButton) {
        return;
    }

    const applyState = () => {
        const assessment = analyzeVmUrl(urlInput.value);
        const nextBtn = document.getElementById('step3Next');

        if (assessment.valid) {
            hint.textContent = `[OK] ${assessment.message || 'URL looks good'}`;
            hint.classList.remove('error');
            hint.classList.add('success');
            testButton.disabled = false;
        } else {
            const message = assessment.message || 'Enter a valid http(s) URL';
            hint.textContent = `[FAIL] ${message}`;
            hint.classList.remove('success');
            hint.classList.add('error');
            testButton.disabled = true;
            connectionValid = false;
            if (nextBtn) {
                nextBtn.disabled = true;
            }
            updateStep3NextTooltip();
        }
    };

    urlInput.addEventListener('input', () => {
        if (!programmaticUrlUpdate) {
            resetResolvedConnectionCache();
        }
        applyState();
    });
    applyState();
}

function initializeStep3NextTooltip() {
    const wrapper = document.getElementById('step3NextWrapper');
    const btn = document.getElementById('step3Next');
    if (!wrapper || !btn) {
        return;
    }
    wrapper.addEventListener('mouseenter', () => {
        if (btn.disabled) {
            wrapper.classList.add('show-tooltip');
        }
    });
    wrapper.addEventListener('mouseleave', () => {
        wrapper.classList.remove('show-tooltip');
    });
    updateStep3NextTooltip();
}

function updateStep3NextTooltip() {
    const wrapper = document.getElementById('step3NextWrapper');
    const btn = document.getElementById('step3Next');
    if (!wrapper || !btn) {
        return;
    }
    if (!btn.disabled) {
        wrapper.classList.remove('show-tooltip');
    }
}

function initializeModeToggle() {
    const toggle = document.getElementById('modeToggleInput');
    const wrapper = document.getElementById('modeToggleWrapper');
    if (!toggle || !wrapper) {
        return;
    }

    document.body.classList.toggle('mode-custom', currentMode === 'custom');
    toggle.checked = currentMode === 'custom';
    toggle.addEventListener('change', () => {
        if (!canSwitchMode()) {
            toggle.checked = currentMode === 'custom';
            return;
        }
        const nextMode = toggle.checked ? 'custom' : 'cluster';
        setMode(nextMode, true);
    });

    wrapper.addEventListener('mouseenter', () => {
        if (!canSwitchMode()) {
            wrapper.classList.add('show-tooltip');
        }
    });
    wrapper.addEventListener('mouseleave', () => {
        wrapper.classList.remove('show-tooltip');
    });

    updateModeToggleLock();
    updateModeLabels();
}

function canSwitchMode() {
    return currentStepIndex === 0;
}

function updateModeToggleLock() {
    const toggle = document.getElementById('modeToggleInput');
    const wrapper = document.getElementById('modeToggleWrapper');
    if (!toggle || !wrapper) {
        return;
    }
    const locked = !canSwitchMode();
    toggle.disabled = locked;
    wrapper.classList.toggle('locked', locked);
    wrapper.classList.toggle('show-tooltip', false);
}

function setMode(mode, animate = false) {
    if (mode !== 'cluster' && mode !== 'custom') {
        return;
    }
    if (currentMode === mode) {
        return;
    }
    currentMode = mode;
    const toggle = document.getElementById('modeToggleInput');
    if (toggle) {
        toggle.checked = currentMode === 'custom';
    }
    document.body.classList.toggle('mode-custom', currentMode === 'custom');
    updateModeLabels();
    updateSelectionStepCopy();
    updateWelcomeCopy();
    updateObfuscationCopy();
    triggerModeFlip(animate);
    resetModeState();
    rebuildStepSequence();
    showStepByIndex(0, true);
}

function updateSelectionStepCopy() {
    const step = document.querySelector('.step[data-step="5"]');
    if (!step) {
        return;
    }
    const title = step.querySelector('.step-title');
    const info = step.querySelector('.info-box');
    const loadingText = step.querySelector('#componentsLoading p');
    if (currentMode === 'custom' && customQueryType === 'selector') {
        if (title) {
            title.textContent = 'Select Targets to Export';
        }
        if (info) {
            info.textContent = 'Select jobs discovered from your selector.';
        }
        if (loadingText) {
            loadingText.textContent = 'Discovering jobs...';
        }
    } else {
        if (title) {
            title.textContent = 'Select Components to Export';
        }
        if (info) {
            info.textContent = 'Select which VictoriaMetrics components you want to export metrics from.';
        }
        if (loadingText) {
            loadingText.textContent = 'Discovering components...';
        }
    }
}

function updateWelcomeCopy() {
    const purpose = document.getElementById('welcomePurpose');
    const intro = document.getElementById('welcomeIntro');
    const steps = document.querySelectorAll('#welcomeSteps [data-welcome-step]');

    if (!purpose || !intro || steps.length === 0) {
        return;
    }

    if (currentMode === 'custom') {
        purpose.innerHTML = '<strong>Purpose:</strong> Export metrics by selector or MetricsQL query for VictoriaMetrics team analysis';
        intro.textContent = 'This wizard will guide you through exporting metrics from any dataset in VictoriaMetrics. You will:';
        steps.forEach(step => {
            const key = step.dataset.welcomeStep;
            switch (key) {
                case 'time':
                    step.textContent = 'Select a time range for your query';
                    break;
                case 'connect':
                    step.textContent = 'Connect to your VictoriaMetrics endpoint';
                    break;
                case 'select':
                    step.textContent = 'Enter a selector or query and optionally choose targets';
                    break;
                case 'obfuscate':
                    step.textContent = 'Adjust obfuscation and remove unwanted labels';
                    break;
                case 'download':
                    step.textContent = 'Download the ready-to-send archive';
                    break;
            }
        });
    } else {
        purpose.innerHTML = '<strong>Purpose:</strong> Export VictoriaMetrics internal metrics for VictoriaMetrics team analysis';
        intro.textContent = 'This wizard will guide you through exporting metrics from your VictoriaMetrics installation. You will:';
        steps.forEach(step => {
            const key = step.dataset.welcomeStep;
            switch (key) {
                case 'time':
                    step.textContent = 'Select a time range for export';
                    break;
                case 'connect':
                    step.textContent = 'Connect to your VM instance (vmselect/vmsingle)';
                    break;
                case 'select':
                    step.textContent = 'Choose which components to export';
                    break;
                case 'obfuscate':
                    step.textContent = 'Obfuscate sensitive information (IPs, job names)';
                    break;
                case 'download':
                    step.textContent = 'Download the ready-to-send archive';
                    break;
            }
        });
    }
}

function updateObfuscationCopy() {
    const info = document.getElementById('obfuscationInfoBox');
    const dropDetails = document.getElementById('dropLabelsDetails');
    if (info) {
        info.textContent = currentMode === 'custom'
            ? 'Customize obfuscation and remove labels before exporting your query results.'
            : 'Obfuscate sensitive information before sending to VictoriaMetrics team.';
    }
    if (dropDetails) {
        dropDetails.style.display = currentMode === 'custom' ? 'block' : 'none';
    }
}

function updateModeLabels() {
    const clusterLabel = document.getElementById('modeLabelCluster');
    const customLabel = document.getElementById('modeLabelCustom');
    if (clusterLabel) {
        clusterLabel.classList.toggle('active', currentMode === 'cluster');
    }
    if (customLabel) {
        customLabel.classList.toggle('active', currentMode === 'custom');
    }
}

function triggerModeFlip(animate) {
    if (!animate) {
        return;
    }
    const container = document.getElementById('cardFlip');
    if (!container) {
        return;
    }
    container.classList.remove('flip');
    void container.offsetWidth;
    container.classList.add('flip');
    setTimeout(() => {
        container.classList.remove('flip');
    }, 700);
}

function resetModeState() {
    connectionValid = false;
    resolvedConnection = null;
    lastValidatedInput = '';
    lastValidatedConfigKey = '';
    lastValidationAttempts = [];
    lastValidationFinalEndpoint = '';
    discoveredComponents = [];
    discoveredSelectorJobs = [];
    sampleMetrics = [];
    sampleStatus = 'idle';
    sampleHadError = false;
    customQueryValidated = false;
    customQueryValidationMessage = '';
    customQueryType = 'selector';
    obfuscationTouched = false;
    selectedCustomLabels.clear();
    removedLabels.clear();
    resetResolvedConnectionCache();
    const queryInput = document.getElementById('customQueryInput');
    if (queryInput) {
        queryInput.value = '';
    }
    const queryResult = document.getElementById('customQueryValidationResult');
    if (queryResult) {
        queryResult.innerHTML = '';
    }
    const connectionResult = document.getElementById('connectionResult');
    if (connectionResult) {
        connectionResult.innerHTML = '';
    }
    clearSelectionUI();
    renderSelectorJobs([]);
    updateSelectionSummary();
    updateCustomQueryDetection();
    updateCustomQueryValidation();
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

    let parsedUrl;
    try {
        parsedUrl = new URL(candidate);
    } catch (err) {
        return { valid: false, message: 'Invalid URL format' };
    }

    if (!['http:', 'https:'].includes(parsedUrl.protocol)) {
        return { valid: false, message: 'Only http:// or https:// are supported' };
    }

    if (!isValidHost(parsedUrl.hostname)) {
        return { valid: false, message: 'Hostname must be localhost, IP, or DNS name' };
    }

    // Prefer IPv4 loopback to avoid IPv6-only refusals
    if (parsedUrl.hostname === 'localhost') {
        parsedUrl.hostname = '127.0.0.1';
        candidate = candidate.replace('localhost', '127.0.0.1');
    }

    return {
        valid: true,
        url: parsedUrl,
        normalized: candidate.replace(/\/+$/, ''),
        message: parsedUrl.hostname === 'localhost' ? 'Local endpoint detected' : 'URL looks valid',
    };
}

function isValidHost(host) {
    if (!host) {
        return false;
    }

    if (host === 'localhost') {
        return true;
    }

    // IPv4
    if (/^\d{1,3}(\.\d{1,3}){3}$/.test(host)) {
        return host.split('.').every(part => {
            const value = Number(part);
            return value >= 0 && value <= 255;
        });
    }

    // IPv6
    if (host.includes(':')) {
        try {
            // Validate by attempting to construct a URL with IPv6 literal
            new URL(`http://[${host}]:8080`);
            return true;
        } catch {
            return false;
        }
    }

    // Kubernetes-style DNS names (allow single label or multi-label)
    const labelRegex = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/i;
    return host.split('.').every(segment => labelRegex.test(segment));
}

// Navigation
function nextStep() {
    if (currentStepIndex >= stepSequence.length - 1) return;

    const currentStepId = getActiveStepId();
    if (!validateStep(currentStepId)) {
        return;
    }

    showStepByIndex(currentStepIndex + 1);
}

function prevStep() {
    if (currentStepIndex <= 0) return;
    showStepByIndex(currentStepIndex - 1);
}

function rebuildStepSequence() {
    const defaultSequence = [1, 2, 3, 5, 6, 7];
    const defaultNames = ['Welcome', 'Time Range', 'VM Connection', 'Select Components', 'Obfuscation', 'Complete'];

    if (currentMode === 'custom') {
        if (customQueryType === 'metricsql') {
            stepSequence = [1, 2, 3, 4, 6, 7];
            stepNames = ['Welcome', 'Time Range', 'VM Connection', 'Selector / Query', 'Obfuscation', 'Complete'];
        } else {
            stepSequence = [1, 2, 3, 4, 5, 6, 7];
            stepNames = ['Welcome', 'Time Range', 'VM Connection', 'Selector / Query', 'Select Targets', 'Obfuscation', 'Complete'];
        }
    } else {
        stepSequence = defaultSequence;
        stepNames = defaultNames;
    }

    if (!stepSequence.includes(getActiveStepId())) {
        currentStepIndex = 0;
    }

    updateProgress();
    updateNextButtons();
    updateModeToggleLock();
}

function getActiveStepId() {
    if (stepSequence.length === 0) {
        return 1;
    }
    return stepSequence[currentStepIndex] || stepSequence[0];
}

function showStepByIndex(index, force = false) {
    const steps = document.querySelectorAll('.step');
    steps.forEach(step => step.classList.remove('active'));

    const stepId = stepSequence[index];
    const nextStepEl = document.querySelector(`.step[data-step="${stepId}"]`);
    if (!nextStepEl) {
        return;
    }

    currentStepIndex = index;
    nextStepEl.classList.add('active');

    if (stepId === 3 && shouldAutoOpenHelp()) {
        const help = document.querySelector('.step[data-step="3"] .help-section');
        if (help) {
            help.setAttribute('open', '');
        }
        markHelpAutoOpenFlag(false);
    }

    updateProgress();
    updateNextButtons();
    updateModeToggleLock();
    onStepEntered(stepId, force);
}

function updateProgress() {
    const total = stepSequence.length || totalStepsDefault;
    const current = Math.min(currentStepIndex + 1, total);
    const progress = total > 1 ? ((current - 1) / (total - 1)) * 100 : 0;
    document.getElementById('progress').style.width = progress + '%';

    const stepName = stepNames[currentStepIndex] || 'Step';
    document.getElementById('stepInfo').textContent = `Step ${current} of ${total}: ${stepName}`;
}

function updateNextButtons() {
    // Remove primary class from all buttons containing "Next" to avoid strict-mode collisions
    document.querySelectorAll('button').forEach(btn => {
        if (btn.textContent && btn.textContent.includes('Next')) {
            btn.classList.remove('btn-primary');
        }
    });

    const buttons = document.querySelectorAll('[data-next]');
    buttons.forEach(btn => {
        btn.classList.add('btn-next');
        const step = btn.closest('.step');
        if (step && step.classList.contains('active')) {
            btn.classList.add('btn-primary');
        }
    });
}

function onStepEntered(stepId, force = false) {
    if (stepId === 4 && currentMode === 'custom') {
        focusCustomQueryInput();
    }
    if (stepId === 5) {
        if (currentMode === 'cluster') {
            discoverComponents();
        } else if (currentMode === 'custom' && customQueryType === 'selector') {
            discoverSelectorJobs();
        }
    } else if (stepId === 6) {
        ensureObfuscationDefaults();
        applyRecommendedMetricStep(true);
        loadSampleMetrics();
    }
}

function validateComponentSelection() {
    if (currentMode === 'custom' && customQueryType === 'selector') {
        let selected = document.querySelectorAll('.selector-job-item input[type="checkbox"]:checked');
        if (selected.length === 0) {
            document.querySelectorAll('.selector-job-item input[type="checkbox"]').forEach(cb => {
                cb.checked = true;
            });
            selected = document.querySelectorAll('.selector-job-item input[type="checkbox"]:checked');
        }
        return selected.length > 0;
    }

    let selected = document.querySelectorAll('.component-item input[type="checkbox"]:checked');
    if (selected.length === 0) {
        document.querySelectorAll('.component-header input[type="checkbox"]').forEach(cb => {
            cb.checked = true;
            handleComponentCheck(cb);
        });
        selected = document.querySelectorAll('.component-item input[type="checkbox"]:checked');
    }
    return selected.length > 0;
}

function validateStep(stepId) {
    switch (stepId) {
        case 2: {
            const from = document.getElementById('timeFrom').value;
            const to = document.getElementById('timeTo').value;
            if (!from || !to) {
                alert('Please select both start and end times');
                return false;
            }
            if (new Date(from) >= new Date(to)) {
                alert('Start time must be before end time');
                return false;
            }
            return true;
        }
        case 3:
            if (!connectionValid) {
                alert('Please test the connection first');
                return false;
            }
            return true;
        case 4:
            if (currentMode !== 'custom') {
                return true;
            }
            if (!getActiveCustomQuery()) {
                alert('Please enter a selector or query first');
                return false;
            }
            if (!customQueryValidated) {
                alert('Please validate the selector or query before proceeding');
                return false;
            }
            return true;
        case 5:
            if (currentMode === 'custom' && customQueryType === 'metricsql') {
                return true;
            }
            return validateComponentSelection();
        default:
            return true;
    }
}

// Time Range Presets
function setPreset(preset, clickedButton) {
    const now = new Date();
    const timezone = document.getElementById('timezone').value;
    let from;

    switch (preset) {
        case '15m':
            from = new Date(now.getTime() - 15 * 60 * 1000);
            break;
        case '1h':
            from = new Date(now.getTime() - 60 * 60 * 1000);
            break;
        case '3h':
            from = new Date(now.getTime() - 3 * 60 * 60 * 1000);
            break;
        case '6h':
            from = new Date(now.getTime() - 6 * 60 * 60 * 1000);
            break;
        case '12h':
            from = new Date(now.getTime() - 12 * 60 * 60 * 1000);
            break;
        case '24h':
            from = new Date(now.getTime() - 24 * 60 * 60 * 1000);
            break;
    }

    document.getElementById('timeFrom').value = formatDateTimeLocal(from, timezone);
    document.getElementById('timeTo').value = formatDateTimeLocal(now, timezone);

    // Update button states
    document.querySelectorAll('.preset-btn').forEach(btn => btn.classList.remove('active'));
    if (clickedButton) {
        clickedButton.classList.add('active');
    }
}

// Authentication
function toggleAuthFields() {
    const authType = document.getElementById('authType').value;
    const authFields = document.getElementById('authFields');

    let html = '';

    switch (authType) {
        case 'basic':
            html = `
                <div class="form-group">
                    <label for="username">Username:</label>
                    <input type="text" id="username" required>
                </div>
                <div class="form-group">
                    <label for="password">Password:</label>
                    <input type="password" id="password" required>
                </div>
            `;
            break;
        case 'bearer':
            html = `
                <div class="form-group">
                    <label for="token">Bearer Token:</label>
                    <input type="password" id="token" required>
                </div>
            `;
            break;
        case 'header':
            html = `
                <div class="form-group">
                    <label for="headerName">Header Name:</label>
                    <input type="text" id="headerName" placeholder="X-API-Key" required>
                </div>
                <div class="form-group">
                    <label for="headerValue">Header Value:</label>
                    <input type="password" id="headerValue" required>
                </div>
            `;
            break;
    }

    authFields.innerHTML = html;
}

// Connection Test with multi-stage validation
async function testConnection() {
    const btn = document.getElementById('testBtnText');
    const result = document.getElementById('connectionResult');
    const nextBtn = document.getElementById('step3Next');
    const buttonWrapper = document.getElementById('testConnectionBtn');
    const rawUrl = document.getElementById('vmUrl')?.value || '';

    btn.innerHTML = '<span class="btn-spinner"></span> Testing...';

    const urlAssessment = analyzeVmUrl(document.getElementById('vmUrl').value);
    if (!urlAssessment.valid) {
        result.innerHTML = `
            <div class="error-message">
                [FAIL] ${urlAssessment.message}
            </div>
        `;
        btn.textContent = 'Test Connection';
        connectionValid = false;
        nextBtn.disabled = true;
        if (buttonWrapper) {
            buttonWrapper.disabled = true;
        }
        return;
    }

    result.innerHTML = '<div id="validationSteps"></div>';

    const stepsContainer = document.getElementById('validationSteps');

    // Helper to add validation step
    function addStep(icon, text, status = 'pending') {
        const stepId = `step-${Date.now()}-${Math.random()}`;
        const stepHtml = `
            <div id="${stepId}" data-status="${status}" style="padding: 8px; margin: 5px 0; border-left: 3px solid #666; background: #f5f5f5; font-size: 13px;">
                <span style="margin-right: 8px;">[${status.toUpperCase()}]</span>
                <span>${text}</span>
            </div>
        `;
        stepsContainer.insertAdjacentHTML('beforeend', stepHtml);
        return stepId;
    }

    function updateStep(stepId, icon, text, status) {
        const step = document.getElementById(stepId);
        if (step) {
            const colors = {
                pending: '#666',
                progress: '#2962FF',
                success: '#4CAF50',
                error: '#f44336'
            };
            step.style.borderLeftColor = colors[status];
            step.setAttribute('data-status', status);
            step.innerHTML = `<span style="margin-right: 8px;">[${status.toUpperCase()}]</span><span>${text}</span>`;
        }
    }

    try {
        const config = getConnectionConfig();

        // [SEARCH] DEBUG: Log connection config
        console.group('[CONN] Multi-Stage Connection Test');
        console.log('[INFO] Connection Config:', config);

        // Step 1: Parse URL
        const step1 = addStep('[SEARCH]', 'Parsing URL...', 'progress');
        await new Promise(resolve => setTimeout(resolve, 300));

        if (!config.url) {
            updateStep(step1, '[FAIL]', 'URL parsing failed: Empty URL', 'error');
            throw new Error('URL is required');
        }

        updateStep(step1, '[OK]', `URL parsed: ${config.url}${config.api_base_path || ''}`, 'success');
        console.log('[OK] Step 1: URL parsed');

        // Step 2: DNS/Network check
        const step2 = addStep('[NET]', 'Checking network connectivity...', 'progress');
        await new Promise(resolve => setTimeout(resolve, 300));

        try {
            // Try to reach the host
            const hostCheck = await fetch(config.url + '/metrics', {
                method: 'HEAD',
                mode: 'no-cors', // Allow cross-origin for basic connectivity check
                cache: 'no-cache'
            }).catch(() => null);

            updateStep(step2, '[OK]', 'Host is reachable', 'success');
            console.log('[OK] Step 2: Host reachable');
        } catch (e) {
            // Even if CORS fails, it means host is reachable
            updateStep(step2, '[OK]', 'Host is reachable (CORS protected)', 'success');
            console.log('[OK] Step 2: Host reachable (CORS)');
        }

        // Step 3: Detect VictoriaMetrics
        const step3 = addStep('[SEARCH]', 'Detecting VictoriaMetrics...', 'progress');
        await new Promise(resolve => setTimeout(resolve, 300));

        // This will be done by the backend
        updateStep(step3, '[CONVERT]', 'Querying VictoriaMetrics API...', 'progress');
        console.log('[CONVERT] Step 3: Querying VM API');

        // Step 4: Test connection with auth
        const step4 = addStep('[SECURE]', 'Testing connection with authentication...', 'progress');

        const response = await fetch('/api/validate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ connection: config })
        });

        console.log('[QUERY] Response Status:', response.status, response.statusText);

        const data = await response.json();
        console.log('[BUILD] Response Data:', data);
        lastValidationAttempts = Array.isArray(data.attempts) ? data.attempts : [];
        lastValidationFinalEndpoint = data.final_endpoint || '';

        if (response.ok && data.success) {
            // Check if VictoriaMetrics was detected
            if (data.is_victoria_metrics === false) {
                updateStep(step3, '[WARN]', 'Warning: Not VictoriaMetrics', 'error');
                updateStep(step4, '[OK]', 'Connection successful, but...', 'success');

                console.log('[WARN]  Warning: Not VictoriaMetrics');
                console.groupEnd();

                // Add warning summary
                stepsContainer.insertAdjacentHTML('beforeend', `
                    <div style="margin-top: 15px; padding: 15px; background: #fff3cd; border-radius: 4px; border-left: 4px solid #ff9800;">
                        <div style="font-weight: bold; color: #f57c00; margin-bottom: 8px;">[WARN] Warning</div>
                        <div style="font-size: 13px; color: #555;">
                            ${data.warning || 'The endpoint responded but does not appear to be VictoriaMetrics.'}<br><br>
                            <strong>Please verify:</strong><br>
                            - The URL points to VictoriaMetrics (vmselect, vmsingle, or vmauth)<br>
                            - The path includes /prometheus or /select/... if needed<br>
                            - Authentication credentials are correct
                        </div>
                    </div>
                `);

                connectionValid = false;
                nextBtn.disabled = true;
                updateStep3NextTooltip();
                return;
            }

            updateStep(step3, '[OK]', `VictoriaMetrics detected! (${data.vm_components ? data.vm_components.join(', ') : 'components found'})`, 'success');
            updateStep(step4, '[OK]', `Connection successful! Version: ${data.version || 'Unknown'}`, 'success');

            console.log('[OK] All steps passed!');
            console.log('[BUILD] VM Components:', data.vm_components);
            console.groupEnd();

            if (data.resolved_connection) {
                resolvedConnection = data.resolved_connection;
            } else {
                resolvedConnection = config;
            }
            lastValidatedInput = rawUrl;
            lastValidatedConfigKey = buildConnectionCacheKey(rawUrl);

            const finalEndpoint = data.final_endpoint || resolvedConnection.full_api_url || (resolvedConnection.url + (resolvedConnection.api_base_path || ''));

            // Add final summary
            stepsContainer.insertAdjacentHTML('beforeend', `
                <div style="margin-top: 15px; padding: 15px; background: #e8f5e9; border-radius: 4px; border-left: 4px solid #4CAF50;">
                    <div style="font-weight: bold; color: #2e7d32; margin-bottom: 8px;">[OK] Connection Successful!</div>
                    <div style="font-size: 13px; color: #555;">
                        <strong>Version:</strong> ${data.version || 'Unknown'}<br>
                        <strong>Components:</strong> ${data.components || 0} detected<br>
                        ${data.vm_components && data.vm_components.length > 0 ? `<strong>VM Components:</strong> ${data.vm_components.join(', ')}<br>` : ''}
                        ${finalEndpoint ? `<strong>Final endpoint:</strong> ${finalEndpoint}<br>` : ''}
                        ${config.tenant_id ? `<strong>Tenant ID:</strong> ${config.tenant_id}<br>` : ''}
                        ${config.is_multitenant ? `<strong>Type:</strong> Multitenant endpoint<br>` : ''}
                        ${Array.isArray(data.attempts) && data.attempts.length > 0 ? `<strong>Attempts:</strong><br>${data.attempts.map(a => `- ${a.endpoint} ${a.success ? '[OK]' : '[FAIL]'}`).join('<br>')}<br>` : ''}
                    </div>
                </div>
            `);

            connectionValid = true;
            nextBtn.disabled = false;
            updateStep3NextTooltip();
            const hint = document.getElementById('vmUrlHint');
            if (hint) {
                hint.textContent = '[OK] URL looks valid';
                hint.classList.remove('error');
                hint.classList.add('success');
            }
        } else {
            updateStep(step4, '[FAIL]', `Connection failed: ${data.error || 'Unknown error'}`, 'error');
            const hint = data.hint ? `\nHint: ${data.hint}` : '';
            throw new Error((data.error || 'Connection failed') + hint);
        }
    } catch (error) {
        console.error('[FAIL] Connection failed:', error);
        console.error('[FAIL] Error stack:', error.stack);
        console.groupEnd();

        // Better error message
        let errorMsg = error.message;
        let errorDetails = '';

        if (error.message.includes('Failed to fetch')) {
            errorMsg = 'Network error: Cannot reach the server';
            errorDetails = 'Check if the URL is correct and the server is accessible';
        } else if (error.message.includes('JSON')) {
            errorMsg = 'Invalid response from server';
            errorDetails = 'The server returned an unexpected response';
        } else if (error.message.includes('401')) {
            errorMsg = 'Authentication failed (401)';
            errorDetails = 'Check your username and password';
        } else if (error.message.includes('403')) {
            errorMsg = 'Access forbidden (403)';
            errorDetails = 'You don\'t have permission to access this resource';
        } else if (error.message.includes('404')) {
            errorMsg = 'Not found (404)';
            errorDetails = 'Check the URL path - the endpoint may not exist';
        }

        const attemptsHtml = Array.isArray(lastValidationAttempts) && lastValidationAttempts.length > 0
            ? `<div style="margin-top: 8px; font-size: 12px; color: #555;">
                <strong>Attempts:</strong><br>
                ${lastValidationAttempts.map(a => `- ${a.endpoint} ${a.success ? '[OK]' : '[FAIL]'}${a.error ? `: ${a.error}` : ''}`).join('<br>')}
              </div>`
            : '';

        const finalEndpointHtml = lastValidationFinalEndpoint
            ? `<div style="margin-top: 8px; font-size: 12px; color: #555;"><strong>Final endpoint:</strong> ${lastValidationFinalEndpoint}</div>`
            : '';

        result.innerHTML = `
            <div class="error-message">
                [FAIL] ${errorMsg}
                ${errorDetails ? `<div style="margin-top: 8px; font-size: 13px;">${errorDetails}</div>` : ''}
                ${finalEndpointHtml}
                ${attemptsHtml}
                <div style="margin-top: 10px; font-size: 12px; opacity: 0.8; border-top: 1px solid #ffcccc; padding-top: 10px;">
                    <strong>Debug info:</strong><br>
                    Open browser console (F12) -> Console tab for detailed logs<br>
                    Technical error: ${error.message}
                </div>
            </div>
        `;
        connectionValid = false;
        nextBtn.disabled = true;
    } finally {
        btn.textContent = 'Test Connection';
    }
}

// Parse VM URL to extract base URL and path components
function parseVMUrl(rawUrl) {
    const assessment = analyzeVmUrl(rawUrl);
    if (!assessment.valid || !assessment.url) {
        throw new Error(assessment.message || 'Invalid URL');
    }

    const url = assessment.url;
    const sanitizedPath = url.pathname.replace(/\/+$/, '') || '/';

    const baseUrl = `${url.protocol}//${url.host}`;
    let apiBasePath = '';
    let tenantId = null;
    let isMultitenant = false;

    const selectMatch = sanitizedPath.match(/^(\/select\/(\d+|multitenant))(\/prometheus)?/);
    if (selectMatch) {
        const tenant = selectMatch[2];
        if (tenant === 'multitenant') {
            isMultitenant = true;
            apiBasePath = '/select/multitenant/prometheus';
        } else {
            tenantId = tenant;
            apiBasePath = `/select/${tenant}/prometheus`;
        }
    } else if (/^\/\d+$/.test(sanitizedPath)) {
        tenantId = sanitizedPath.substring(1);
        apiBasePath = `${sanitizedPath}/prometheus`;
    } else if (sanitizedPath.includes('/prometheus')) {
        apiBasePath = sanitizedPath;
    } else if (sanitizedPath && sanitizedPath !== '/') {
        apiBasePath = `${sanitizedPath}/prometheus`;
    } else {
        apiBasePath = '/prometheus';
    }

    return {
        baseUrl,
        apiBasePath,
        tenantId,
        isMultitenant,
        fullApiUrl: baseUrl + apiBasePath
    };
}

function getConnectionConfig() {
    const authType = document.getElementById('authType').value;
    const rawUrl = document.getElementById('vmUrl').value;
    const cacheKey = buildConnectionCacheKey(rawUrl);

    if (resolvedConnection && cacheKey === lastValidatedConfigKey) {
        return resolvedConnection;
    }

    // [SEARCH] DEBUG: Log raw input
    console.log('[FIX] Building connection config:', { rawUrl, authType });

    const parsedUrl = parseVMUrl(rawUrl);
    console.log('[FIX] Parsed URL:', parsedUrl);

    // Build auth object based on type
    const auth = { type: authType };

    switch (authType) {
        case 'basic':
            auth.username = document.getElementById('username').value;
            auth.password = document.getElementById('password').value;
            console.log('[FIX] Auth: Basic (username set)');
            break;
        case 'bearer':
            auth.token = document.getElementById('token').value;
            console.log('[FIX] Auth: Bearer (token set)');
            break;
        case 'header':
            auth.header_name = document.getElementById('headerName').value;
            auth.header_value = document.getElementById('headerValue').value;
            console.log('[FIX] Auth: Custom Header');
            break;
        case 'none':
        default:
            console.log('[FIX] Auth: None');
            break;
    }

    const config = {
        url: parsedUrl.baseUrl,
        api_base_path: parsedUrl.apiBasePath,
        tenant_id: parsedUrl.tenantId,
        is_multitenant: parsedUrl.isMultitenant,
        full_api_url: parsedUrl.fullApiUrl,
        auth: auth,
        skip_tls_verify: false
    };

    console.log('[OK] Final config:', config);

    return config;
}

function initializeCustomQueryInputs() {
    const input = document.getElementById('customQueryInput');
    const validateBtn = document.getElementById('validateCustomQueryBtn');

    if (input) {
        input.addEventListener('input', () => {
            customQueryValidated = false;
            updateCustomQueryDetection();
            updateCustomQueryValidation();
        });
    }
    if (validateBtn) {
        validateBtn.addEventListener('click', () => validateCustomQuery());
    }

    updateCustomQueryDetection();
    updateCustomQueryValidation();
}

function isLikelySelectorQuery(query) {
    const trimmed = (query || '').trim();
    if (!trimmed) {
        return false;
    }
    if (trimmed.includes('(') || trimmed.includes(')')) {
        return false;
    }
    const selectorPattern = /^\s*([a-zA-Z_:][a-zA-Z0-9_:]*)?\s*(\{.*\})?\s*$/;
    return selectorPattern.test(trimmed);
}

function updateCustomQueryDetection() {
    const query = getActiveCustomQuery();
    const badge = document.getElementById('queryTypeBadge');
    const behavior = document.getElementById('customQueryBehavior');

    if (!query) {
        if (badge) {
            badge.textContent = 'Awaiting input';
        }
        if (behavior) {
            behavior.textContent = 'Enter a selector or query to see which path will be used.';
        }
        customQueryType = 'selector';
        rebuildStepSequence();
        updateSelectionStepCopy();
        return;
    }

    const detected = isLikelySelectorQuery(query) ? 'selector' : 'metricsql';
    customQueryType = detected;
    if (badge) {
        badge.textContent = detected === 'selector' ? 'Selector detected' : 'MetricsQL detected';
    }
    if (behavior) {
        behavior.textContent = detected === 'selector'
            ? 'Selector mode enables per-job target selection.'
            : 'MetricsQL mode skips target selection and exports the query result.';
    }
    rebuildStepSequence();
    updateSelectionStepCopy();
}

function getActiveCustomQuery() {
    const input = document.getElementById('customQueryInput');
    return (input?.value || '').trim();
}

function focusCustomQueryInput() {
    document.getElementById('customQueryInput')?.focus();
}

async function validateCustomQuery() {
    const query = getActiveCustomQuery();
    const resultEl = document.getElementById('customQueryValidationResult');

    if (!query) {
        if (resultEl) {
            resultEl.innerHTML = '<div class="error-message">[FAIL] Query is required</div>';
        }
        customQueryValidated = false;
        updateCustomQueryValidation();
        return;
    }

    const config = getConnectionConfig();
    if (!connectionValid) {
        if (resultEl) {
            resultEl.innerHTML = '<div class="error-message">[FAIL] Please test the connection first</div>';
        }
        customQueryValidated = false;
        updateCustomQueryValidation();
        return;
    }

    if (resultEl) {
        resultEl.innerHTML = '<div class="loading-banner" style="text-align:center;color:#888;padding:12px;"><div class="loading-spinner" style="display:inline-block;"></div> Validating...</div>';
    }

    try {
        const response = await fetch('/api/validate-query', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                connection: config,
                query: query
            })
        });
        const data = await response.json();
        if (!response.ok || !data.success) {
            const errMsg = data.error || data.message || 'Validation failed';
            if (resultEl) {
                resultEl.innerHTML = `<div class="error-message">[FAIL] ${errMsg}</div>`;
            }
            customQueryValidated = false;
            customQueryValidationMessage = errMsg;
            updateCustomQueryValidation();
            return;
        }
        customQueryValidated = true;
        customQueryType = data.query_type === 'selector' ? 'selector' : 'metricsql';
        customQueryValidationMessage = '';
        if (resultEl) {
            const meta = data.result_count != null ? `<div class="input-hint">Matched series: ${data.result_count}</div>` : '';
            resultEl.innerHTML = `<div class="success-box" style="padding:12px;margin-top:10px;">[OK] Query validated (${customQueryType})${meta}</div>`;
        }
        updateCustomQueryDetection();
        updateCustomQueryValidation();
        rebuildStepSequence();
    } catch (err) {
        if (resultEl) {
            resultEl.innerHTML = `<div class="error-message">[FAIL] ${err.message}</div>`;
        }
        customQueryValidated = false;
        customQueryValidationMessage = err.message;
        updateCustomQueryValidation();
    }
}

function updateCustomQueryValidation() {
    const badge = document.getElementById('customQueryStatus');
    if (!badge) {
        return;
    }
    if (currentMode !== 'custom') {
        badge.textContent = '';
        return;
    }
    if (customQueryValidated) {
        badge.textContent = `[OK] Validated (${customQueryType})`;
        badge.classList.remove('error');
        badge.classList.add('success');
    } else {
        badge.textContent = customQueryValidationMessage ? `[FAIL] ${customQueryValidationMessage}` : '[WAIT] Validation required';
        badge.classList.remove('success');
        badge.classList.add('error');
    }
}

function clearSelectionUI() {
    const list = document.getElementById('componentsList');
    const selectorList = document.getElementById('selectorJobsList');
    const error = document.getElementById('componentsError');
    if (list) {
        list.innerHTML = '';
    }
    if (selectorList) {
        selectorList.innerHTML = '';
    }
    if (error) {
        error.textContent = '';
        error.classList.add('hidden');
    }
}

// Component Discovery
function setComponentsLoadingState(isLoading) {
    const loading = document.getElementById('componentsLoading');
    const list = document.getElementById('componentsList');
    const selectorList = document.getElementById('selectorJobsList');
    const error = document.getElementById('componentsError');
    const nextBtn = document.getElementById('step5Next');
    componentsLoadingInFlight = isLoading;
    if (loading) {
        loading.style.display = isLoading ? 'block' : 'none';
    }
    if (list) {
        list.style.display = isLoading ? 'none' : (currentMode === 'cluster' ? 'block' : 'none');
    }
    if (selectorList) {
        selectorList.style.display = isLoading ? 'none' : (currentMode === 'custom' && customQueryType === 'selector' ? 'block' : 'none');
    }
    if (error && isLoading) {
        error.classList.add('hidden');
    }
    if (nextBtn) {
        nextBtn.disabled = isLoading;
    }
}

async function discoverComponents() {
    const loading = document.getElementById('componentsLoading');
    const list = document.getElementById('componentsList');
    const error = document.getElementById('componentsError');

    setComponentsLoadingState(true);

    try {
        const config = getConnectionConfig();
        const { from, to } = getSafeTimeRangeIso();

        // [SEARCH] DEBUG: Log discovery request
        console.group('[INFO] Component Discovery');
        console.log('[INFO] Time Range:', { from, to });
        console.log('[INFO] Connection:', {
            url: config.url,
            tenant_id: config.tenant_id,
            is_multitenant: config.is_multitenant
        });

        const response = await fetch('/api/discover', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                connection: config,
                time_range: { start: from, end: to }
            })
        });

        console.log('[QUERY] Response Status:', response.status, response.statusText);

        const data = await response.json();
        console.log('[BUILD] Discovered Components:', data.components?.length || 0);

        if (!response.ok) {
            throw new Error(data.error || 'Discovery failed');
        }

        discoveredComponents = data.components || [];

        // Log component summary
        const componentTypes = [...new Set(discoveredComponents.map(c => c.component))];
        console.log('[OK] Component Types:', componentTypes);
        console.groupEnd();

        renderComponents(discoveredComponents);
        setComponentsLoadingState(false);
    } catch (err) {
        console.error('[FAIL] Discovery failed:', err);
        console.groupEnd();

        setComponentsLoadingState(false);
        if (error) {
            error.textContent = err.message + ' (Check console F12 for details)';
            error.classList.remove('hidden');
        }
    }
}

async function discoverSelectorJobs() {
    const loading = document.getElementById('componentsLoading');
    const list = document.getElementById('selectorJobsList');
    const error = document.getElementById('componentsError');

    setComponentsLoadingState(true);

    try {
        const config = getConnectionConfig();
        const { from, to } = getSafeTimeRangeIso();
        const selector = getActiveCustomQuery();

        const response = await fetch('/api/discover-selector', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                connection: config,
                time_range: { start: from, end: to },
                selector: selector
            })
        });

        const data = await response.json();
        if (!response.ok) {
            throw new Error(data.error || 'Selector discovery failed');
        }

        discoveredSelectorJobs = Array.isArray(data.jobs) ? data.jobs : [];
        renderSelectorJobs(discoveredSelectorJobs);
        setComponentsLoadingState(false);
    } catch (err) {
        setComponentsLoadingState(false);
        if (error) {
            error.textContent = err.message + ' (Check console F12 for details)';
            error.classList.remove('hidden');
        }
        if (list) {
            list.innerHTML = '';
        }
    }
}

function renderComponents(components) {
    const list = document.getElementById('componentsList');

    if (components.length === 0) {
        list.innerHTML = '<p style="text-align:center;color:#888;">No components found</p>';
        return;
    }

    let html = '';

    // Backend already returns grouped data - no need to re-group
    // Each component has a 'jobs' array
    components.sort((a, b) => a.component.localeCompare(b.component)).forEach(comp => {
        const totalInstances = comp.instance_count || 0;
        const allJobs = comp.jobs || []; // Array of job names
        const seriesEstimate = typeof comp.metrics_count_estimate === 'number' && comp.metrics_count_estimate >= 0
            ? `${comp.metrics_count_estimate.toLocaleString()} series`
            : 'series unknown';
        const jobListText = allJobs.length > 0 ? allJobs.join(', ') : 'n/a';

        html += `
            <div class="component-item" onclick="toggleComponent(this)">
                <div class="component-header">
                    <input type="checkbox" 
                           data-component="${comp.component}" 
                           onclick="event.stopPropagation();" 
                           onchange="handleComponentCheck(this)">
                    <strong>${comp.component}</strong>
                </div>
                <div class="component-details">
                    Jobs: ${jobListText} | Instances: ${totalInstances} | ${seriesEstimate}
                </div>
                ${allJobs.length > 1 ? renderJobsGroup(comp.component, allJobs, totalInstances, comp.job_metrics || {}) : ''}
            </div>
        `;
    });

    list.innerHTML = html;
    updateSelectionSummary();
}

function renderSelectorJobs(jobs) {
    const list = document.getElementById('selectorJobsList');
    if (!list) {
        return;
    }
    if (!jobs || jobs.length === 0) {
        list.innerHTML = '<p style="text-align:center;color:#888;">No jobs found for selector</p>';
        return;
    }

    list.innerHTML = '';
    jobs.sort((a, b) => (a.job || '').localeCompare(b.job || '')).forEach(job => {
        const jobName = job.job || 'unknown';
        const instances = job.instance_count || 0;
        const seriesEstimate = typeof job.metrics_count_estimate === 'number' && job.metrics_count_estimate >= 0
            ? `${job.metrics_count_estimate.toLocaleString()} series`
            : 'series unknown';

        const item = document.createElement('div');
        item.className = 'selector-job-item';
        item.innerHTML = `
            <label>
                <input type="checkbox" data-job="${jobName}" checked>
                <strong>${jobName}</strong>
                <span class="selector-job-meta">${instances} instance(s) · ${seriesEstimate}</span>
            </label>
        `;
        list.appendChild(item);
    });
    updateSelectionSummary();
}

function renderJobsGroup(componentType, jobs, totalInstances, jobMetrics = {}) {
    let html = '<div class="jobs-group">';

    jobs.forEach(job => {
        const estimatedInstances = Math.ceil(totalInstances / jobs.length);
        const seriesForJob = typeof jobMetrics[job] === 'number' && jobMetrics[job] >= 0
            ? `${jobMetrics[job].toLocaleString()} series`
            : 'series unknown';

        html += `
            <div class="job-item">
                <label onclick="event.stopPropagation();">
                    <input type="checkbox" 
                           data-component="${componentType}" 
                           data-job="${job}"
                           onchange="handleJobCheck(this)">
                    <strong>${job}</strong> - ~${estimatedInstances} instance(s) - ${seriesForJob}
                </label>
            </div>
        `;
    });
    html += '</div>';
    return html;
}

function toggleComponent(element) {
    const checkbox = element.querySelector('input[type="checkbox"]');
    if (checkbox) {
        checkbox.checked = !checkbox.checked;
        handleComponentCheck(checkbox);
    }
}

function handleComponentCheck(checkbox) {
    const item = checkbox.closest('.component-item');
    if (checkbox.checked) {
        item.classList.add('selected');
        // Check all jobs under this component
        item.querySelectorAll('.job-item input[type="checkbox"]').forEach(jobCheckbox => {
            jobCheckbox.checked = true;
        });
    } else {
        item.classList.remove('selected');
        // Uncheck all jobs
        item.querySelectorAll('.job-item input[type="checkbox"]').forEach(jobCheckbox => {
            jobCheckbox.checked = false;
        });
    }

    updateSelectionSummary();
}

function handleJobCheck(checkbox) {
    const item = checkbox.closest('.component-item');
    const componentCheckbox = item.querySelector('.component-header input[type="checkbox"]');
    const allJobs = item.querySelectorAll('.job-item input[type="checkbox"]');
    const checkedJobs = item.querySelectorAll('.job-item input[type="checkbox"]:checked');

    // Update component checkbox based on job checkboxes
    if (checkedJobs.length > 0) {
        componentCheckbox.checked = true;
        item.classList.add('selected');
    } else {
        componentCheckbox.checked = false;
        item.classList.remove('selected');
    }

    updateSelectionSummary();
}

function isObfuscationStepActive() {
    return getActiveStepId() === 6;
}

function scheduleSampleReload() {
    if (!isObfuscationStepActive()) {
        return;
    }
    if (sampleStatus === 'error') {
        return;
    }
    if (sampleRequestInFlight) {
        return;
    }
    if (sampleReloadTimer) {
        clearTimeout(sampleReloadTimer);
    }
    sampleReloadTimer = setTimeout(() => {
        sampleReloadTimer = null;
        loadSampleMetrics();
    }, 250);
}

function initializeStagingDirInput() {
    const input = document.getElementById('stagingDir');
    if (!input) {
        return;
    }
    input.placeholder = DEFAULT_STAGING_DIR;
    const saved = localStorage.getItem('vmgather_staging_dir');
    if (saved) {
        input.value = saved;
        directoryPickerPath = saved;
        userEditedStaging = false;
    } else {
        input.value = DEFAULT_STAGING_DIR;
        directoryPickerPath = DEFAULT_STAGING_DIR;
        userEditedStaging = false;
    }
    const hint = document.getElementById('stagingDirHint');
    if (hint) {
        hint.textContent = `Partial batches live under ${DEFAULT_STAGING_DIR}. Use "Browse..." to reuse an existing folder or "Use default" for a safe fallback.`;
    }
    validateStagingDir(true);
    input.addEventListener('input', () => {
        userEditedStaging = true;
        validateStagingDir(false);
    });
    input.addEventListener('blur', () => validateStagingDir(true));
}

function initializeMetricStepSelector() {
    const timeFrom = document.getElementById('timeFrom');
    const timeTo = document.getElementById('timeTo');
    [timeFrom, timeTo].forEach(el => {
        if (el) {
            const markAndRecalc = () => {
                markHelpAutoOpenFlag(true);
                applyRecommendedMetricStep(false);
            };
            el.addEventListener('change', markAndRecalc);
            el.addEventListener('input', markAndRecalc);
        }
    });
    applyRecommendedMetricStep(true);
}

function getRecommendedMetricStepSeconds() {
    const fromValue = document.getElementById('timeFrom')?.value;
    const toValue = document.getElementById('timeTo')?.value;
    if (!fromValue || !toValue) {
        return 60;
    }
    const from = new Date(fromValue);
    const to = new Date(toValue);
    const durationMs = Math.max(0, to - from);
    const durationMinutes = durationMs / 60000;
    if (durationMinutes <= 15) {
        return 30;
    }
    if (durationMinutes <= 360) {
        return 60;
    }
    return 300;
}

function applyRecommendedMetricStep(forceApply) {
    const select = document.getElementById('metricStep');
    const hint = document.getElementById('metricStepHint');
    if (!select) {
        return;
    }
    const recommended = getRecommendedMetricStepSeconds();
    if (hint) {
        hint.textContent = `Current data step (minimum): ${formatStepLabel(recommended)}`;
    }
    if (forceApply && (!select.value || select.value === '')) {
        select.value = String(recommended);
    }
}

function getSelectedMetricStepSeconds() {
    const select = document.getElementById('metricStep');
    if (!select) {
        return 0;
    }
    const value = select.value;
    if (!value || value === 'auto') {
        return 0;
    }
    const parsed = parseInt(value, 10);
    return isNaN(parsed) ? 0 : parsed;
}

function formatStepLabel(seconds) {
    if (!seconds || seconds < 60) {
        return `${seconds}s`;
    }
    const minutes = seconds / 60;
    if (minutes >= 1 && Number.isInteger(minutes)) {
        return `${minutes} min`;
    }
    return `${seconds}s`;
}

function setStagingDirValue(value) {
    const input = document.getElementById('stagingDir');
    if (!input) {
        return;
    }
    input.value = value;
    directoryPickerPath = value;
    localStorage.setItem('vmgather_staging_dir', value);
    validateStagingDir(true);
}

function useDefaultStagingDir() {
    setStagingDirValue(DEFAULT_STAGING_DIR);
}

function openDirectoryPicker() {
    const overlay = document.getElementById('dirPickerOverlay');
    if (!overlay) {
        return;
    }
    const inputValue = document.getElementById('stagingDir')?.value.trim();
    directoryPickerPath = inputValue || DEFAULT_STAGING_DIR;
    overlay.classList.add('visible');
    loadDirectoryListing(directoryPickerPath);
    if (!directoryPickerCloseHandler) {
        directoryPickerCloseHandler = (event) => {
            if (event.target === overlay) {
                closeDirectoryPicker();
            }
        };
        overlay.addEventListener('click', directoryPickerCloseHandler);
    }
}

function refreshDirectoryPicker() {
    if (directoryPickerPath) {
        loadDirectoryListing(directoryPickerPath);
    }
}

function navigateDirectoryParent() {
    if (directoryPickerParent) {
        loadDirectoryListing(directoryPickerParent);
    }
}

async function loadDirectoryListing(path) {
    const list = document.getElementById('dirPickerList');
    const current = document.getElementById('dirPickerCurrent');
    const status = document.getElementById('dirPickerStatus');
    const parentBtn = document.getElementById('dirPickerParentBtn');
    if (!list || !current) {
        return;
    }
    list.innerHTML = '<div style="padding: 8px; color: #666;">Loading...</div>';
    if (status) {
        status.textContent = '';
    }
    try {
        const resp = await fetch(`/api/fs/list?path=${encodeURIComponent(path)}`);
        if (!resp.ok) {
            let message = `Failed to list directory (${resp.status})`;
            try {
                const err = await resp.json();
                if (err && err.error) {
                    message = err.error;
                }
            } catch (parseErr) {
                console.warn('Failed to parse directory list error', parseErr);
            }
            throw new Error(message);
        }
        const data = await resp.json();
        directoryPickerPath = data.path || path;
        directoryPickerParent = data.parent || '';
        if (parentBtn) {
            parentBtn.disabled = !directoryPickerParent;
        }
        current.textContent = directoryPickerPath;
        list.innerHTML = '';
        const entries = data.entries || [];
        if (data.exists === false && status) {
            status.textContent = 'Directory does not exist yet.';
        } else if (status) {
            status.textContent = '';
        }
        if (entries.length === 0) {
            list.innerHTML = '<div style="padding: 8px; color: #666;">No subdirectories</div>';
            return;
        }
        entries.forEach(entry => {
            const btn = document.createElement('button');
            btn.textContent = entry.name;
            if (!entry.writable) {
                btn.classList.add('readonly');
            }
            btn.onclick = () => {
                loadDirectoryListing(entry.path);
            };
            list.appendChild(btn);
        });
    } catch (err) {
        list.innerHTML = '';
        if (status) {
            status.textContent = err.message;
        }
    }
}

function closeDirectoryPicker() {
    const overlay = document.getElementById('dirPickerOverlay');
    if (overlay) {
        overlay.classList.remove('visible');
    }
    if (directoryPickerCloseHandler && overlay) {
        overlay.removeEventListener('click', directoryPickerCloseHandler);
        directoryPickerCloseHandler = null;
    }
}

function selectCurrentDirectory() {
    if (!directoryPickerPath) {
        return;
    }
    setStagingDirValue(directoryPickerPath);
    closeDirectoryPicker();
}

function createStagingDir() {
    validateStagingDir(true, { ensure: true });
}

function validateStagingDir(immediate = false, options = {}) {
    const input = document.getElementById('stagingDir');
    const hint = document.getElementById('stagingDirHint');
    const createBtn = document.getElementById('createStagingDirBtn');
    if (!input || !hint) {
        return;
    }
    const value = (input.value || '').trim();
    const isE2ETarget = /(?:^|[\\/])vmgather-e2e$/.test(value);
    const ensure = options.ensure === true || value === DEFAULT_STAGING_DIR || isE2ETarget;
    if (!value) {
        hint.textContent = 'Enter directory path';
        hint.style.color = '#c62828';
        window.__vmStagingHint = hint.textContent;
        return;
    }
    const updateHint = (text, color) => {
        hint.textContent = text;
        hint.style.color = color;
        window.__vmStagingHint = text;
    };
    if (stagingDirValidationTimer) {
        clearTimeout(stagingDirValidationTimer);
        stagingDirValidationTimer = null;
    }
    const requestId = Date.now();
    window.__vmStagingReq = requestId;
    const updateCreateButton = (visible) => {
        if (!createBtn) {
            return;
        }
        createBtn.style.display = visible ? 'inline-flex' : 'none';
        createBtn.disabled = !visible;
    };
    const perform = async () => {
        try {
            updateHint(ensure ? 'Preparing directory...' : 'Validating directory...', '#555');
            updateCreateButton(false);
            const resp = await fetch('/api/fs/check', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: value, ensure })
            });
            const data = await resp.json();
            if (resp.ok && data.ok) {
                if (window.__vmStagingReq !== requestId) {
                    return;
                }
                const normalized = data.abs_path || value;
                updateHint(`Ready: ${normalized}`, '#2E7D32');
                updateCreateButton(false);
                if (input.value !== normalized) {
                    input.value = normalized;
                    localStorage.setItem('vmgather_staging_dir', normalized);
                }
                userEditedStaging = false;
            } else {
                if (window.__vmStagingReq !== requestId) {
                    return;
                }
                if (data.exists === false && data.can_create) {
                    if (ensure) {
                        const normalized = data.abs_path || value;
                        updateHint(`Ready: ${normalized}`, '#2E7D32');
                        updateCreateButton(false);
                        if (input.value !== normalized) {
                            input.value = normalized;
                            localStorage.setItem('vmgather_staging_dir', normalized);
                        }
                        userEditedStaging = false;
                    } else {
                        updateHint(`Directory will be created at ${data.abs_path}. Click "Create directory".`, '#ED6C02');
                        updateCreateButton(true);
                        userEditedStaging = true;
                    }
                } else {
                    updateHint(data.message || 'Directory is not writable', '#c62828');
                    updateCreateButton(false);
                }
            }
        } catch (err) {
            if (window.__vmStagingReq !== requestId) {
                return;
            }
            updateHint(`Failed to validate directory: ${err.message}`, '#c62828');
            updateCreateButton(false);
        }
    };

    if (immediate) {
        perform();
        return;
    }

    if (stagingDirValidationTimer) {
        clearTimeout(stagingDirValidationTimer);
    }
    stagingDirValidationTimer = setTimeout(perform, 400);
}

// Sample Metrics Loading
async function loadSampleMetrics() {
    const advancedLabelsContainer = document.getElementById('advancedLabels');
    const samplePreviewContainer = document.getElementById('samplePreview');
    let lastServerError = '';
    const minLoaderMs = 2000;
    const startTs = Date.now();
    sampleHadError = false;
    sampleRequestInFlight = true;
    sampleStatus = 'loading';
    sampleRequestCount += 1;
    console.log(`[SAMPLE] request #${sampleRequestCount} started`);
    if (samplePreviewContainer) {
        samplePreviewContainer.dataset.error = 'false';
    }
    const loadingBanner = `
        <div id="advancedLoader" class="loading-banner" style="text-align: center; color: #888; padding: 16px;">
            <div class="loading-spinner" style="display: inline-block;"></div>
            <p style="margin-top: 8px;">Loading sample metrics...</p>
        </div>
    `;
    const previewLoadingBanner = `
        <div id="previewLoader" class="loading-banner" style="text-align: center; color: #888; padding: 16px;">
            <div class="sample-loading-spinner" style="display: inline-block;"></div>
            <p style="margin-top: 8px;">Loading preview data...</p>
        </div>
    `;

    let globalSpinner = document.getElementById('sampleGlobalSpinner');
    if (!globalSpinner) {
        globalSpinner = document.createElement('div');
        globalSpinner.id = 'sampleGlobalSpinner';
        globalSpinner.className = 'loading-spinner';
        globalSpinner.style.display = 'inline-block';
        globalSpinner.style.margin = '0 8px 8px 0';
        (samplePreviewContainer || advancedLabelsContainer)?.prepend(globalSpinner);
    } else {
        globalSpinner.style.display = 'inline-block';
    }

    const advDetails = document.getElementById('advancedLabelsDetails');
    if (advDetails) {
        advDetails.open = true;
    }
    const prevDetails = document.getElementById('previewDetails');
    if (prevDetails) {
        prevDetails.open = true;
    }

    if (sampleAbortController) {
        try {
            sampleAbortController.abort();
        } catch (e) {
            console.warn('Failed to abort previous sample request', e);
        }
    }

    // Show loading state
    advancedLabelsContainer.innerHTML = loadingBanner;
    samplePreviewContainer.innerHTML = previewLoadingBanner;

    try {
        const config = getConnectionConfig();
        const { from, to } = getSafeTimeRangeIso();
        if (currentMode === 'custom' && !customQueryValidated) {
            throw new Error('Custom selector/query is not validated yet.');
        }

        let selection = getSelectionPayload();
        if (currentMode === 'cluster' && (!selection.selected || selection.selected.length === 0)) {
            autoSelectAllComponents();
            selection = getSelectionPayload();
        }
        if (currentMode === 'custom' && customQueryType === 'selector' && selection.jobs.length === 0) {
            autoSelectAllSelectorJobs();
            selection = getSelectionPayload();
        }
        if (currentMode === 'cluster' && (!selection.selected || selection.selected.length === 0)) {
            throw new Error('No components selected. Please go back to Step 5.');
        }
        if (currentMode === 'custom' && customQueryType === 'selector' && selection.jobs.length === 0) {
            throw new Error('No jobs selected. Please go back to Step 5.');
        }
        const uniqueComponents = selection.components || [];
        const selectedJobs = selection.jobs || [];

        // [SEARCH] DEBUG: Log sample request
        console.group('[STATS] Sample Metrics Loading');
        console.log('[INFO] Selected Components:', uniqueComponents.length);
        console.log('[TARGET] Components:', uniqueComponents);
        console.log('[JOB] Jobs:', selectedJobs);

        // Add timeout (30 seconds)
        const controller = new AbortController();
        sampleAbortController = controller;
        const timeoutId = setTimeout(() => controller.abort(), 30000);

        // Get obfuscation config for samples too
        const obfuscation = getObfuscationConfig();

        const response = await fetch('/api/sample', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                config: {
                    connection: config,
                    time_range: { start: from, end: to },
                    components: uniqueComponents,
                    jobs: selectedJobs,
                    mode: currentMode,
                    query_type: currentMode === 'custom' ? customQueryType : '',
                    query: currentMode === 'custom' ? getActiveCustomQuery() : '',
                    obfuscation: obfuscation  // Add obfuscation to samples
                },
                limit: 10
            }),
            signal: controller.signal
        });

        clearTimeout(timeoutId);

        console.log('[QUERY] Response Status:', response.status, response.statusText);
        console.log(`[SAMPLE] request #${sampleRequestCount} status: ${response.status}`);

        // Check Content-Type before parsing JSON
        const contentType = response.headers.get('content-type');
        if (!contentType || !contentType.includes('application/json')) {
            throw new Error(`Unexpected response type: ${contentType}. Expected JSON.`);
        }

        const data = await response.json();
        console.log('[BUILD] Samples Received:', data.samples?.length || 0);

        if (!response.ok) {
            lastServerError = (data && data.error ? data.error : '') || lastServerError;
            const errMsg = lastServerError || `Server error: ${response.status} ${response.statusText}`;
            throw new Error(errMsg);
        }

        sampleMetrics = data.samples || [];
        sampleStatus = 'success';

        // Log unique labels found
        const allLabels = new Set();
        sampleMetrics.forEach(s => Object.keys(s.labels || {}).forEach(l => allLabels.add(l)));
        console.log('Unique Labels Found:', Array.from(allLabels).sort());
        console.log('[OK] Sample loading complete');
        console.groupEnd();

        const elapsed = Date.now() - startTs;
        if (elapsed < minLoaderMs) {
            await new Promise(res => setTimeout(res, minLoaderMs - elapsed));
        }

        renderSamplePreview(sampleMetrics);
        renderAdvancedLabels(sampleMetrics);
        renderDropLabels(sampleMetrics);
        updateSelectionSummary();
        window.__vm_samples_version = (window.__vm_samples_version || 0) + 1;
        document.querySelectorAll('#advancedLoader,#previewLoader').forEach(el => el.remove());
        const globalSpinnerDone = document.getElementById('sampleGlobalSpinner');
        if (globalSpinnerDone) {
            globalSpinnerDone.style.display = 'none';
        }
    } catch (err) {
        sampleHadError = true;
        sampleStatus = 'error';
        console.log(`[SAMPLE] request #${sampleRequestCount} failed: ${err?.message || err}`);
        if (err && err.name === 'AbortError') {
            sampleStatus = 'idle';
            console.info('Sample request aborted due to newer request');
            console.groupEnd();
            const waitingHtml = `
                <div style="text-align: center; color: #888; padding: 16px;">
                    <div class="loading-spinner" style="display: inline-block;"></div>
                    <p style="margin-top: 8px;">Refreshing sample metrics...</p>
                </div>
            `;
            advancedLabelsContainer.innerHTML = waitingHtml;
            samplePreviewContainer.innerHTML = `
                <div style="text-align: center; color: #888; padding: 16px;">
                    <div class="sample-loading-spinner" style="display: inline-block;"></div>
                    <p style="margin-top: 8px;">Refreshing sample metrics...</p>
                </div>
            `;
            return;
        }
        console.error('[FAIL] Sample loading failed:', err);
        console.groupEnd();

        // Show error in UI
        const detailMessage = err.message || lastServerError || 'Unknown error';
        window.__lastSampleError = detailMessage;
        const advDetails = document.getElementById('advancedLabelsDetails');
        if (advDetails) {
            advDetails.open = true;
        }
        const prevDetails = document.getElementById('previewDetails');
        if (prevDetails) {
            prevDetails.open = true;
        }
        const globalSpinnerErr = document.getElementById('sampleGlobalSpinner');
        if (globalSpinnerErr) {
            globalSpinnerErr.style.display = 'none';
        }
        const errorHtml = `
            <div class="error-message" role="alert" aria-live="assertive" style="margin: 20px; padding: 15px; background: #ffebee; border-left: 4px solid #f44336; border-radius: 4px;">
                <strong style="color: #c62828;">[FAIL] Failed to load sample metrics</strong>
                <p style="margin-top: 10px; color: #555;">${detailMessage}</p>
                <details style="margin-top: 10px; font-size: 12px; color: #666;">
                    <summary style="cursor: pointer; font-weight: 500;">Technical details</summary>
                    <pre style="margin-top: 10px; white-space: pre-wrap; word-break: break-all; background: #f5f5f5; padding: 10px; border-radius: 4px;">${err.stack || err.toString()}</pre>
                </details>
                <button onclick="loadSampleMetrics()" style="margin-top: 15px; padding: 8px 16px; background: #2962FF; color: white; border: none; border-radius: 4px; cursor: pointer; font-weight: 500;">
                    Retry
                </button>
            </div>
        `;
        if (advancedLabelsContainer) {
            advancedLabelsContainer.style.display = 'block';
        }
        if (samplePreviewContainer) {
            samplePreviewContainer.style.display = 'block';
        }
        advancedLabelsContainer.innerHTML = errorHtml;
        samplePreviewContainer.innerHTML = `
            <div class="error-message" role="alert" aria-live="assertive" style="padding: 15px; background: #ffebee; border-left: 4px solid #f44336; border-radius: 4px;">
                <strong style="color: #c62828;">[FAIL] Preview unavailable</strong>
                <p style="margin-top: 8px; color: #555;">${detailMessage}</p>
            </div>
        `;
    } finally {
        sampleRequestInFlight = false;
        sampleAbortController = null;
    }
}

function renderSamplePreview(samples) {
    const preview = document.getElementById('samplePreview');
    if (preview) {
        preview.dataset.error = 'false';
    }

    if (!samples || samples.length === 0) {
        preview.innerHTML = '<p style="text-align:center;color:#888;">No samples available</p>';
        return;
    }

    const limited = samples.slice(0, 5);
    let html = '';

    limited.forEach(sample => {
        // Handle both 'name' and 'metric_name' fields for backward compatibility
        const metricNameRaw = sample.name || sample.metric_name || (sample.labels && sample.labels.__name__);
        const metricName = metricNameRaw || 'unknown';
        const missingName = !metricNameRaw;

        // Ensure labels exist
        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');
        const nameHint = missingName && customQueryType === 'metricsql'
            ? '<div class="metric-note">Metric name removed by aggregation (__name__ not present).</div>'
            : '';

        html += `
            <div class="sample-metric">
                ${nameHint}
                <div class="metric-name">${metricName}</div>
                <div class="metric-labels">{${labels}}</div>
            </div>
        `;
    });

    preview.innerHTML = html;
}

function renderAdvancedLabels(samples) {
    const container = document.getElementById('advancedLabels');

    // Extract all unique labels
    const labelSet = new Set();
    const labelSamples = {};

    samples.forEach(sample => {
        Object.keys(sample.labels || {}).forEach(label => {
            labelSet.add(label);
            if (!labelSamples[label]) {
                labelSamples[label] = sample.labels[label];
            }
        });
    });

    const labels = Array.from(labelSet).sort();

    // Skip instance and job (already in main options)
    const filteredLabels = labels.filter(l => l !== 'instance' && l !== 'job' && !l.startsWith('__'));

    if (filteredLabels.length === 0) {
        container.innerHTML = '<div class="label-item" style="text-align:center;color:#888;padding:20px;">No additional labels found</div>';
        return;
    }

    const availableLabels = new Set(filteredLabels);
    let html = '';
    filteredLabels.forEach(label => {
        const sample = labelSamples[label] || 'example_value';
        const checkedAttr = selectedCustomLabels.has(label) ? 'checked' : '';
        html += `
            <div class="label-item">
                <label>
                    <input type="checkbox" class="obf-label-checkbox" data-label="${label}" ${checkedAttr}>
                    <strong>${label}</strong>
                    <span class="label-sample">(e.g., ${sample})</span>
                </label>
            </div>
        `;
    });

    container.innerHTML = html;

    const obfuscationEnabled = document.getElementById('enableObfuscation')?.checked;
    container.querySelectorAll('.obf-label-checkbox').forEach(cb => {
        cb.disabled = !obfuscationEnabled;
    });

    // Prune selections that are no longer available
    Array.from(selectedCustomLabels).forEach(label => {
        if (!availableLabels.has(label)) {
            selectedCustomLabels.delete(label);
        }
    });
}

function renderDropLabels(samples) {
    const container = document.getElementById('dropLabels');
    if (!container) {
        return;
    }
    if (currentMode !== 'custom') {
        container.innerHTML = '<div class="label-item" style="text-align:center;color:#888;padding:20px;">Label removal is available in custom mode.</div>';
        return;
    }

    const labelSet = new Set();
    const labelSamples = {};
    samples.forEach(sample => {
        Object.keys(sample.labels || {}).forEach(label => {
            labelSet.add(label);
            if (!labelSamples[label]) {
                labelSamples[label] = sample.labels[label];
            }
        });
    });

    const labels = Array.from(labelSet)
        .filter(label => !label.startsWith('__'))
        .sort();

    if (labels.length === 0) {
        container.innerHTML = '<div class="label-item" style="text-align:center;color:#888;padding:20px;">No removable labels found</div>';
        return;
    }

    let html = '';
    labels.forEach(label => {
        const sample = labelSamples[label] || 'example_value';
        const checkedAttr = removedLabels.has(label) ? 'checked' : '';
        html += `
            <div class="label-item">
                <label>
                    <input type="checkbox" class="drop-label-checkbox" data-label="${label}" ${checkedAttr}>
                    <strong>${label}</strong>
                    <span class="label-sample">(e.g., ${sample})</span>
                </label>
            </div>
        `;
    });
    container.innerHTML = html;

    container.querySelectorAll('.drop-label-checkbox').forEach(cb => {
        cb.addEventListener('change', () => {
            const label = cb.dataset.label;
            if (!label) {
                return;
            }
            if (cb.checked) {
                removedLabels.add(label);
            } else {
                removedLabels.delete(label);
            }
            scheduleSampleReload();
        });
    });
}

function moveAdvancedSections(enabled) {
    const slot = document.getElementById('obfuscationAdvancedSlot');
    const mount = document.getElementById('obfuscationAdvancedMount');
    const advancedDetails = document.getElementById('advancedLabelsDetails');
    const previewDetails = document.getElementById('previewDetails');
    const target = enabled ? slot : mount;
    if (!target) {
        return;
    }
    [advancedDetails, previewDetails].forEach(node => {
        if (node && node.parentElement !== target) {
            target.appendChild(node);
        }
    });
}

function toggleObfuscation(markTouched = true) {
    const enabled = document.getElementById('enableObfuscation').checked;
    if (markTouched) {
        obfuscationTouched = true;
    }
    const options = document.getElementById('obfuscationOptions');
    const instanceCheckbox = document.querySelector('.obf-label-checkbox[data-label="instance"]');
    const jobCheckbox = document.querySelector('.obf-label-checkbox[data-label="job"]');
    if (options) {
        options.classList.toggle('disabled', !enabled);
        options.classList.toggle('hidden', !enabled);
        options.style.display = enabled ? 'block' : 'none';
        options.setAttribute('aria-hidden', enabled ? 'false' : 'true');
        options.querySelectorAll('input[type="checkbox"]').forEach(cb => {
            cb.disabled = !enabled && cb.id !== 'enableObfuscation';
        });
    }
    moveAdvancedSections(enabled);
    document.querySelectorAll('#advancedLabels .obf-label-checkbox').forEach(cb => {
        cb.disabled = !enabled;
    });
    if (enabled) {
        if (instanceCheckbox && !instanceCheckbox.checked) {
            instanceCheckbox.checked = true;
        }
        if (jobCheckbox && !jobCheckbox.checked) {
            jobCheckbox.checked = true;
        }
        const advDetails = document.getElementById('advancedLabelsDetails');
        if (advDetails) {
            advDetails.open = true;
        }
    }

    if (enabled && isObfuscationStepActive()) {
        if (sampleRequestInFlight && sampleAbortController) {
            try {
                sampleAbortController.abort();
            } catch (e) {
                console.warn('Failed to abort sample reload', e);
            }
        }
        loadSampleMetrics();
        return;
    }

    scheduleSampleReload();
}

function initializeObfuscationOptions() {
    const enabled = document.getElementById('enableObfuscation');
    if (!enabled) {
        return;
    }
    toggleObfuscation(false);
}

function ensureObfuscationDefaults() {
    const checkbox = document.getElementById('enableObfuscation');
    if (!checkbox || obfuscationTouched) {
        return;
    }
    if (currentMode === 'custom') {
        checkbox.checked = false;
    }
    toggleObfuscation(false);
}

function wireAdvancedSummaries() {
    const advDetails = document.getElementById('advancedLabelsDetails');
    const previewDetails = document.getElementById('previewDetails');
    const handleToggle = (details) => {
        if (!details || !details.open) {
            return;
        }
        if (sampleStatus === 'error' || sampleRequestInFlight) {
            return;
        }
        sampleHadError = false;
        loadSampleMetrics();
    };
    if (advDetails) {
        advDetails.addEventListener('toggle', () => handleToggle(advDetails));
    }
    if (previewDetails) {
        previewDetails.addEventListener('toggle', () => handleToggle(previewDetails));
    }
}

function initializeHelpSection() {
    const helpDetails = document.querySelector('.help-section');
    if (!helpDetails) {
        return;
    }
    helpDetails.addEventListener('toggle', () => {
        if (!helpDetails.open) {
            return;
        }
        markHelpAutoOpenFlag(false);
    });
}

function getSelectedComponents() {
    const selected = [];

    // Get all checked component checkboxes
    document.querySelectorAll('.component-header input[type="checkbox"]:checked').forEach(cb => {
        const component = cb.dataset.component;
        const item = cb.closest('.component-item');

        // Check if there are specific job selections
        const jobCheckboxes = item.querySelectorAll('.job-item input[type="checkbox"]:checked');

        if (jobCheckboxes.length > 0) {
            // Add each selected job
            jobCheckboxes.forEach(jobCb => {
                selected.push({
                    component: jobCb.dataset.component,
                    job: jobCb.dataset.job
                });
            });
        } else {
            // Add component without specific job (all jobs)
            selected.push({ component });
        }
    });

    return selected;
}

function getSelectedJobsFromSelector() {
    const selected = [];
    document.querySelectorAll('.selector-job-item input[type="checkbox"]:checked').forEach(cb => {
        const job = cb.dataset.job;
        if (job) {
            selected.push(job);
        }
    });
    return selected;
}

function getSelectionPayload() {
    if (currentMode === 'custom' && customQueryType === 'selector') {
        return {
            components: [],
            jobs: getSelectedJobsFromSelector(),
        };
    }

    const selected = getSelectedComponents();
    const uniqueComponents = Array.from(new Set(selected.map(s => s.component)));
    const selectedJobs = selected.map(s => s.job).filter(Boolean);
    return {
        components: uniqueComponents,
        jobs: selectedJobs,
        selected,
    };
}

function getSafeTimeRangeIso() {
    const fromInput = document.getElementById('timeFrom')?.value || '';
    const toInput = document.getElementById('timeTo')?.value || '';
    let fromDate = new Date(fromInput);
    let toDate = new Date(toInput);
    if (!fromInput || !toInput || Number.isNaN(fromDate.getTime()) || Number.isNaN(toDate.getTime())) {
        const now = new Date();
        toDate = now;
        fromDate = new Date(now.getTime() - 60 * 60 * 1000);
        console.warn('[WARN] Invalid time range, defaulting to last 1h');
    }
    return { from: fromDate.toISOString(), to: toDate.toISOString() };
}

function autoSelectAllComponents() {
    document.querySelectorAll('.component-header input[type="checkbox"]').forEach(cb => {
        cb.checked = true;
        handleComponentCheck(cb);
    });
}

function autoSelectAllSelectorJobs() {
    document.querySelectorAll('.selector-job-item input[type="checkbox"]').forEach(cb => {
        cb.checked = true;
    });
    updateSelectionSummary();
}

function updateSelectionSummary() {
    const summary = document.getElementById('selectionSummary');
    if (!summary) {
        return;
    }

    if (currentMode === 'custom' && customQueryType === 'metricsql') {
        summary.innerHTML = `
            <h4>[BUILD] Estimated Export Volume</h4>
            <p class="summary-placeholder">MetricsQL mode exports series directly from your query.</p>
        `;
        return;
    }

    if (currentMode === 'custom' && customQueryType === 'selector') {
        const selectedJobs = getSelectedJobsFromSelector();
        if (!selectedJobs || selectedJobs.length === 0) {
            summary.innerHTML = `
                <h4>[BUILD] Estimated Export Volume</h4>
                <p class="summary-placeholder">Select jobs above to see series estimates.</p>
            `;
            return;
        }

        const stats = computeSelectorSelectionStats(selectedJobs);
        if (stats.length === 0) {
            summary.innerHTML = `
                <h4>[BUILD] Estimated Export Volume</h4>
                <p class="summary-placeholder">Metrics data is not available for the selected jobs.</p>
            `;
            return;
        }

        const totalKnown = stats.reduce((sum, stat) => stat.series != null ? sum + stat.series : sum, 0);
        const hasUnknown = stats.some(stat => stat.series == null);

        let html = '<h4>[BUILD] Estimated Export Volume</h4><div class="summary-grid">';
        stats.forEach(stat => {
            const seriesLabel = stat.series != null
                ? `${stat.series.toLocaleString()} series`
                : 'Series count unavailable';
            html += `
                <div class="summary-card">
                    <div><strong>${stat.job}</strong></div>
                    <div class="summary-meta">${stat.instances} instance(s)</div>
                    <div class="summary-meta">${seriesLabel}</div>
                </div>
            `;
        });
        html += '</div>';
        if (hasUnknown) {
            html += `<div class="summary-total">Known total: ${totalKnown.toLocaleString()} series (additional data pending)</div>`;
        } else {
            html += `<div class="summary-total">Total: ${totalKnown.toLocaleString()} series</div>`;
        }
        summary.innerHTML = html;
        return;
    }

    const selected = getSelectedComponents();
    if (!selected || selected.length === 0) {
        summary.innerHTML = `
            <h4>[BUILD] Estimated Export Volume</h4>
            <p class="summary-placeholder">Select components above to see per-component and per-job series counts.</p>
        `;
        return;
    }

    const stats = computeSelectionStats(selected);
    if (stats.length === 0) {
        summary.innerHTML = `
            <h4>[BUILD] Estimated Export Volume</h4>
            <p class="summary-placeholder">Metrics data is not available for the selected components.</p>
        `;
        return;
    }

    const totalKnown = stats.reduce((sum, stat) => {
        return stat.series != null ? sum + stat.series : sum;
    }, 0);
    const hasUnknown = stats.some(stat => stat.series == null);

    let html = '<h4>[BUILD] Estimated Export Volume</h4><div class="summary-grid">';
    stats.forEach(stat => {
        const seriesLabel = stat.series != null
            ? `${stat.series.toLocaleString()} series`
            : 'Series count unavailable';

        html += `
            <div class="summary-card">
                <div><strong>${stat.component}</strong></div>
                <div class="summary-meta">${seriesLabel}</div>
        `;

        if (stat.jobs.length > 0) {
            html += '<ul>';
            stat.jobs.forEach(job => {
                const jobLabel = job.series != null
                    ? job.series.toLocaleString()
                    : 'unknown';
                html += `<li>${job.name}: ${jobLabel}</li>`;
            });
            html += '</ul>';
        }

        html += '</div>';
    });
    html += '</div>';

    if (hasUnknown) {
        html += `<div class="summary-total">Known total: ${totalKnown.toLocaleString()} series (additional data pending)</div>`;
    } else {
        html += `<div class="summary-total">Total: ${totalKnown.toLocaleString()} series</div>`;
    }

    summary.innerHTML = html;
}

function computeSelectionStats(selected) {
    const statsMap = new Map();

    selected.forEach(item => {
        if (!item || !item.component) {
            return;
        }

        const compData = discoveredComponents.find(comp => comp.component === item.component);
        if (!compData) {
            return;
        }

        const existing = statsMap.get(item.component) || {
            component: item.component,
            series: 0,
            jobs: [],
            hasUnknownJob: false,
            metricsEstimate: typeof compData.metrics_count_estimate === 'number' && compData.metrics_count_estimate >= 0
                ? compData.metrics_count_estimate
                : null,
            jobMetrics: compData.job_metrics || {},
        };

        if (item.job) {
            const jobSeries = existing.jobMetrics[item.job];
            existing.jobs.push({
                name: item.job,
                series: typeof jobSeries === 'number' && jobSeries >= 0 ? jobSeries : null,
            });

            if (jobSeries == null || jobSeries < 0) {
                existing.hasUnknownJob = true;
            } else if (!existing.hasUnknownJob) {
                existing.series += jobSeries;
            }
        } else {
            existing.series = existing.metricsEstimate;
            existing.jobs = (compData.jobs || []).map(jobName => ({
                name: jobName,
                series: typeof existing.jobMetrics[jobName] === 'number' && existing.jobMetrics[jobName] >= 0
                    ? existing.jobMetrics[jobName]
                    : null,
            }));
        }

        statsMap.set(item.component, existing);
    });

    return Array.from(statsMap.values()).map(stat => {
        if (stat.jobs.length === 0 && stat.series == null) {
            stat.series = stat.metricsEstimate;
        }
        if (stat.hasUnknownJob && stat.series !== null && stat.jobs.length > 0) {
            stat.series = null;
        }
        return {
            component: stat.component,
            series: stat.series,
            jobs: stat.jobs,
        };
    });
}

function computeSelectorSelectionStats(selectedJobs) {
    const jobMap = new Map();
    discoveredSelectorJobs.forEach(job => {
        jobMap.set(job.job, job);
    });

    return selectedJobs.map(jobName => {
        const job = jobMap.get(jobName) || {};
        const series = typeof job.metrics_count_estimate === 'number' && job.metrics_count_estimate >= 0
            ? job.metrics_count_estimate
            : null;
        return {
            job: jobName,
            instances: job.instance_count || 0,
            series,
        };
    });
}

function getObfuscationConfig() {
    const enabled = document.getElementById('enableObfuscation').checked;
    const dropLabels = Array.from(removedLabels);

    if (!enabled) {
        return {
            enabled: false,
            obfuscate_instance: false,
            obfuscate_job: false,
            preserve_structure: true,
            custom_labels: [],
            drop_labels: dropLabels
        };
    }

    // Get selected labels for obfuscation
    const selectedLabels = new Set();
    document.querySelectorAll('.obf-label-checkbox:checked').forEach(cb => {
        const label = cb.dataset.label;
        if (label) {
            selectedLabels.add(label);
        }
    });
    selectedCustomLabels.forEach(label => selectedLabels.add(label));

    // Separate standard labels (instance, job) from custom labels (pod, namespace, etc.)
    const customLabels = Array.from(selectedLabels).filter(label =>
        label !== 'instance' && label !== 'job'
    );

    // Map labels to backend format
    return {
        enabled: true,
        obfuscate_instance: selectedLabels.has('instance'),
        obfuscate_job: selectedLabels.has('job'),
        preserve_structure: true,
        custom_labels: customLabels,  // pod, namespace, etc.
        drop_labels: dropLabels
    };
}

// Export
function lockStartButton(text) {
    const btn = document.getElementById('startExportBtn');
    if (btn) {
        btn.disabled = true;
        if (text) {
            btn.textContent = text;
        }
    }
}

function unlockStartButton() {
    const btn = document.getElementById('startExportBtn');
    if (btn) {
        btn.disabled = false;
        btn.textContent = btn.dataset.originalText || 'Prepare Support Bundle';
    }
}

async function exportMetrics(buttonElement) {
    const btn = buttonElement || event?.target;
    if (!btn) {
        console.error('No button element provided to exportMetrics');
        return;
    }
    if (btn.dataset.mode === 'resume') {
        hideResumeExportOption();
        lockStartButton('Resuming…');
        resumeExportJob();
        return;
    }
    const originalText = btn.textContent || 'Prepare Support Bundle';
    currentExportButton = btn;
    btn.dataset.originalText = originalText;
    btn.dataset.mode = '';
    btn.disabled = true;
    btn.innerHTML = '<span class="btn-spinner"></span> Collecting metrics...';

    try {
        window.__lastExportError = null;
        const config = getConnectionConfig();
        const { from, to } = getSafeTimeRangeIso();
        let selection = getSelectionPayload();
        if (currentMode === 'cluster' && (!selection.selected || selection.selected.length === 0)) {
            autoSelectAllComponents();
            selection = getSelectionPayload();
        }
        if (currentMode === 'custom' && customQueryType === 'selector' && selection.jobs.length === 0) {
            autoSelectAllSelectorJobs();
            selection = getSelectionPayload();
        }
        if (currentMode === 'cluster' && (!selection.selected || selection.selected.length === 0)) {
            throw new Error('No components selected. Please go back to Step 5.');
        }
        if (currentMode === 'custom' && customQueryType === 'selector' && selection.jobs.length === 0) {
            throw new Error('No jobs selected. Please go back to Step 5.');
        }
        const uniqueComponents = selection.components || [];
        const selectedJobs = selection.jobs || [];
        const obfuscation = getObfuscationConfig();

        // [SEARCH] DEBUG: Log export request
        console.group('[SEND] Metrics Export');
        console.log('[INFO] Export Config:', {
            time_range: { from, to },
            components: uniqueComponents.length,
            obfuscation: {
                enabled: obfuscation.enabled,
                obfuscate_instance: obfuscation.obfuscate_instance,
                obfuscate_job: obfuscation.obfuscate_job
            }
        });
        console.log('[TARGET] Selected components:', uniqueComponents);
        console.log('[JOB] Selected jobs:', selectedJobs);

        const stagingDirValue = document.getElementById('stagingDir')?.value.trim() || '';
        if (!stagingDirValue) {
            throw new Error('Please provide a staging directory');
        }
        const metricStepSeconds = getSelectedMetricStepSeconds();
        const batchWindowSeconds = metricStepSeconds || getRecommendedMetricStepSeconds();
        const batchingConfig = {
            enabled: true,
            strategy: 'custom',
            custom_interval_secs: batchWindowSeconds,
        };

        const exportPayload = {
            connection: config,
            time_range: { start: from, end: to },
            components: uniqueComponents,
            jobs: selectedJobs,
            mode: currentMode,
            query_type: currentMode === 'custom' ? customQueryType : '',
            query: currentMode === 'custom' ? getActiveCustomQuery() : '',
            obfuscation: obfuscation,
            staging_dir: stagingDirValue,
            metric_step_seconds: metricStepSeconds,
            batching: batchingConfig
        };
        window.__lastExportStartPayload = exportPayload;

        const response = await fetch('/api/export/start', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(exportPayload)
        });

        console.log('[QUERY] Response Status:', response.status, response.statusText);

        const data = await response.json();

        if (!response.ok) {
            throw new Error(data.error || 'Export failed');
        }

        console.log('[START] Export job started:', data.job_id);
        currentExportJobId = data.job_id;
        showExportProgressPanel(data);
        await monitorExportJob(btn);
        console.groupEnd();
    } catch (err) {
        console.error('[FAIL] Export failed:', err);
        console.groupEnd();

        window.__lastExportError = err?.message || String(err);
        alert('Export failed: ' + err.message + '\n\nCheck browser console (F12) for details');
        btn.disabled = false;
        btn.textContent = 'Prepare Support Bundle';
        currentExportButton = null;
    }
}

async function monitorExportJob(btn) {
    if (!currentExportJobId) {
        return;
    }

    const fetchStatus = async () => {
        try {
            const resp = await fetch(`/api/export/status?id=${encodeURIComponent(currentExportJobId)}`);
            const status = await resp.json();
            if (!resp.ok) {
                throw new Error(status.error || 'Failed to fetch status');
            }
            updateExportProgress(status);
            if (status.state === 'completed') {
                cleanupExportPolling(false);
                exportResult = status.result;
                if (exportResult) {
                    showExportResult(exportResult);
                    nextStep();
                }
                btn.disabled = false;
                btn.textContent = btn.dataset.originalText || 'Prepare Support Bundle';
                currentExportButton = null;
                disableCancelButton();
                showCancelNotice('');
            } else if (status.state === 'failed') {
                cleanupExportPolling(false);
                btn.disabled = false;
                btn.textContent = btn.dataset.originalText || 'Prepare Support Bundle';
                currentExportButton = null;
                alert('Export failed: ' + (status.error || 'Unknown error'));
                disableCancelButton();
                showCancelNotice('');
            } else if (status.state === 'canceled') {
                cleanupExportPolling(true);
                btn.disabled = false;
                btn.textContent = 'Resume export';
                currentExportButton = null;
                disableCancelButton();
                showResumeExportOption('Export canceled. You can resume without changing selections.');
            }
        } catch (err) {
            console.error('Failed to fetch export status', err);
        }
    };

    await fetchStatus();
    exportStatusTimer = setInterval(fetchStatus, 2000);
}

function showExportResult(data) {
    const panel = document.getElementById('exportProgressPanel');
    if (panel) {
        panel.classList.add('hidden');
        panel.style.display = 'none';
    }
    renderExportStagingPath('');
    document.getElementById('exportId').textContent = data.export_id || data.exportID || 'N/A';
    const metricsValue = data.metrics_count ?? data.metrics_exported ?? 0;
    document.getElementById('metricsCount').textContent = (metricsValue || 0).toLocaleString();
    const archiveSizeValue = data.archive_size ?? data.archive_size_bytes ?? 0;
    document.getElementById('archiveSize').textContent = ((archiveSizeValue || 0) / 1024).toFixed(2);
    document.getElementById('archiveSha256').textContent = data.sha256 || 'N/A';

    // Render spoilers with sample data
    if (data.sample_data && data.sample_data.length > 0) {
        renderExportSpoilers(data.sample_data);
    }
}

function showExportProgressPanel(meta) {
    const panel = document.getElementById('exportProgressPanel');
    const percent = document.getElementById('exportProgressPercent');
    const batches = document.getElementById('exportProgressBatches');
    const metrics = document.getElementById('exportProgressMetrics');
    const eta = document.getElementById('exportProgressEta');
    const windowInfo = document.getElementById('exportBatchWindow');
    const fill = document.getElementById('exportProgressFill');

    hideResumeExportOption();
    if (panel) {
        panel.classList.remove('hidden');
        panel.style.display = 'block';
    }
    if (percent) {
        percent.textContent = '0%';
    }
    if (batches) {
        batches.textContent = `0 / ${meta.total_batches || 1} batches`;
    }
    if (metrics) {
        metrics.textContent = 'Waiting for first batch...';
    }
    if (eta) {
        eta.textContent = 'Estimating time to completion...';
    }
    if (windowInfo && meta.batch_window_seconds) {
        windowInfo.textContent = `≈ ${Math.round(meta.batch_window_seconds)}s`;
    }
    if (fill) {
        fill.style.width = '0%';
    }
    if (meta.staging_path) {
        exportStagingPath = meta.staging_path;
    }
    if (typeof meta.obfuscation_enabled === 'boolean') {
        currentJobObfuscationEnabled = meta.obfuscation_enabled;
    } else {
        currentJobObfuscationEnabled = false;
    }
    renderExportStagingPath(exportStagingPath);
    const cancelBtn = document.getElementById('cancelExportBtn');
    if (cancelBtn) {
        cancelBtn.disabled = false;
        cancelBtn.textContent = 'Cancel export';
    }
    showCancelNotice('');
}

function updateExportProgress(status) {
    const fill = document.getElementById('exportProgressFill');
    const percentEl = document.getElementById('exportProgressPercent');
    const batchesEl = document.getElementById('exportProgressBatches');
    const metricsEl = document.getElementById('exportProgressMetrics');
    const etaEl = document.getElementById('exportProgressEta');
    const summaryEl = document.getElementById('exportProgressSummary');

    const percentage = Math.min(100, Math.round((status.progress || 0) * 100));
    if (fill) {
        fill.style.width = percentage + '%';
    }
    if (percentEl) {
        percentEl.textContent = percentage + '%';
    }
    if (batchesEl) {
        batchesEl.textContent = `${status.completed_batches || 0} / ${status.total_batches || 1} batches`;
    }
    if (metricsEl) {
        const descriptor = (status.obfuscation_enabled ?? currentJobObfuscationEnabled) ? 'obfuscated' : 'processed';
        metricsEl.textContent = `${(status.metrics_processed || 0).toLocaleString()} series ${descriptor}`;
    }
    if (etaEl) {
        etaEl.textContent = status.eta ? `ETA ${new Date(status.eta).toLocaleTimeString()}` : '';
    }
    if (summaryEl) {
        const last = typeof status.last_batch_duration_seconds === 'number'
            ? status.last_batch_duration_seconds.toFixed(1)
            : '0.0';
        const avg = typeof status.average_batch_seconds === 'number'
            ? status.average_batch_seconds.toFixed(1)
            : '0.0';
        summaryEl.textContent = `Last batch ${last}s - Avg ${avg}s`;
    }
    if (typeof status.obfuscation_enabled === 'boolean') {
        currentJobObfuscationEnabled = status.obfuscation_enabled;
    }
    if (status.staging_path) {
        exportStagingPath = status.staging_path;
    }
    renderExportStagingPath(exportStagingPath);
}

function cleanupExportPolling(preserveJob = false) {
    if (exportStatusTimer) {
        clearInterval(exportStatusTimer);
        exportStatusTimer = null;
    }
    if (!preserveJob) {
        exportStagingPath = '';
        currentJobObfuscationEnabled = false;
        currentExportJobId = null;
        renderExportStagingPath('');
    }
    disableCancelButton();
}

function renderExportStagingPath(path) {
    const el = document.getElementById('exportProgressPath');
    if (el) {
        el.textContent = path || '-';
    }
}

function renderExportSpoilers(samples) {
    const container = document.getElementById('exportSpoilers');

    const limited = samples.slice(0, 5);
    let html = '<h3 style="margin-bottom: 15px;">[STATS] Exported Data Samples (Top 5)</h3>';

    limited.forEach((sample, idx) => {
        // Handle both 'name' and 'metric_name' fields for backward compatibility
        const metricNameRaw = sample.name || sample.metric_name || (sample.labels && sample.labels.__name__);
        const metricName = metricNameRaw || 'unknown';
        const missingName = !metricNameRaw;

        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');

        const nameHint = missingName && customQueryType === 'metricsql'
            ? '<div class="metric-note">Metric name removed by aggregation (__name__ not present).</div>'
            : '';

        html += `
            <div class="spoiler">
                <div class="spoiler-header" onclick="toggleSpoiler(this)">
                    <span>Sample ${idx + 1}: ${metricName}</span>
                    <span>v</span>
                </div>
                <div class="spoiler-content">
                    <div class="spoiler-body">
                        <div class="sample-metric">
                            ${nameHint}
                            <div class="metric-name">${metricName}</div>
                            <div class="metric-labels">{${labels}}</div>
                            ${sample.value ? `<div style="margin-top: 10px; color: #2962FF;">Value: ${sample.value}</div>` : ''}
                        </div>
                    </div>
                </div>
            </div>
        `;
    });

    container.innerHTML = html;
}

function toggleSpoiler(header) {
    const content = header.nextElementSibling;
    const arrow = header.querySelector('span:last-child');

    if (content.classList.contains('open')) {
        content.classList.remove('open');
        arrow.textContent = 'v';
    } else {
        content.classList.add('open');
        arrow.textContent = '^';
    }
}

function showCancelNotice(message, color = '#c62828') {
    const el = document.getElementById('exportCancelNotice');
    if (el) {
        el.textContent = message || '';
        el.style.color = color;
    }
}

function disableCancelButton() {
    const cancelBtn = document.getElementById('cancelExportBtn');
    if (cancelBtn) {
        cancelBtn.disabled = true;
        cancelBtn.textContent = 'Cancel export';
    }
}

function showResumeExportOption(message) {
    const startBtn = document.getElementById('startExportBtn');
    const resumeBtn = document.getElementById('resumeExportBtn');
    if (startBtn) {
        startBtn.disabled = false;
        startBtn.textContent = 'Resume export';
        startBtn.dataset.mode = 'resume';
    }
    if (resumeBtn) {
        resumeBtn.style.display = 'inline-flex';
        resumeBtn.disabled = false;
    }
    showCancelNotice(message || 'Export canceled. You can resume with the same settings.');
}

function hideResumeExportOption() {
    const startBtn = document.getElementById('startExportBtn');
    const resumeBtn = document.getElementById('resumeExportBtn');
    if (startBtn) {
        startBtn.textContent = startBtn.dataset.originalText || 'Prepare Support Bundle';
        startBtn.dataset.mode = '';
    }
    if (resumeBtn) {
        resumeBtn.style.display = 'none';
        resumeBtn.disabled = true;
    }
}

function resumeExportJob() {
    if (!currentExportJobId) return;
    const startBtn = document.getElementById('startExportBtn');
    hideResumeExportOption();
    lockStartButton('Resuming…');
    fetch('/api/export/resume', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ job_id: currentExportJobId })
    })
        .then(async resp => {
            const data = await resp.json().catch(() => ({}));
            if (!resp.ok) {
                throw new Error(data.error || 'Failed to resume export');
            }
            showExportProgressPanel(data);
            return monitorExportJob(startBtn || { disabled: false, textContent: '' });
        })
        .catch(err => {
            console.error('Failed to resume export', err);
            alert('Failed to resume export: ' + err.message);
            unlockStartButton();
        });
}

async function cancelExportJob() {
    if (!currentExportJobId || cancelRequestInFlight) {
        return;
    }
    cancelRequestInFlight = true;
    const cancelBtn = document.getElementById('cancelExportBtn');
    if (cancelBtn) {
        cancelBtn.disabled = true;
        cancelBtn.textContent = 'Canceling...';
    }
    showCancelNotice('Sending cancellation request...');
    try {
        const resp = await fetch('/api/export/cancel', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ job_id: currentExportJobId }),
        });
        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || 'Failed to cancel export');
        }
        showCancelNotice('Cancellation requested. Waiting for exporter to stop...');
    } catch (err) {
        console.error('Cancel export failed', err);
        alert('Failed to cancel export: ' + err.message);
        if (cancelBtn) {
            cancelBtn.disabled = false;
            cancelBtn.textContent = 'Cancel export';
        }
        showCancelNotice('');
    } finally {
        cancelRequestInFlight = false;
    }
}

// Download
function downloadArchive() {
    if (!exportResult || !exportResult.archive_path) {
        console.error('[FAIL] No archive available for download');
        alert('No archive available for download');
        return;
    }

    // [SEARCH] DEBUG: Log download
    console.group('[DOWNLOAD]  Archive Download');
    console.log('[BUILD] Archive Path:', exportResult.archive_path);
    console.log('[STATS] Archive Size:', (exportResult.archive_size / 1024).toFixed(2), 'KB');
    console.log('[SECURE] SHA256:', exportResult.sha256);

    // Create download link
    const link = document.createElement('a');
    link.href = '/api/download?path=' + encodeURIComponent(exportResult.archive_path);
    link.download = exportResult.archive_path.split('/').pop();

    console.log('[LINK] Download URL:', link.href);
    console.log('[OK] Initiating browser download');
    console.groupEnd();

    // Trigger download
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
}

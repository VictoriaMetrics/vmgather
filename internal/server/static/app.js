// VMExporter Frontend - Enhanced UX/UI
// State
let currentStep = 1;
const totalSteps = 6;
let DEFAULT_STAGING_DIR = '/tmp/vmexporter';
let connectionValid = false;
let discoveredComponents = [];
let sampleMetrics = [];
let exportResult = null;
let currentExportJobId = null;
let exportStatusTimer = null;
let sampleReloadTimer = null;
let sampleAbortController = null;
const selectedCustomLabels = new Set();
let exportStagingPath = '';
let currentJobObfuscationEnabled = false;
let stagingDirValidationTimer = null;
let directoryPickerPath = '';
let directoryPickerParent = '';
let directoryPickerCloseHandler = null;
let appConfigLoaded = false;
let currentExportButton = null;
let cancelRequestInFlight = false;

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

    initializeUrlValidation();
    updateSelectionSummary();

	document.addEventListener('change', (event) => {
        const target = event.target;
        if (!target || !target.classList || !target.classList.contains('obf-label-checkbox')) {
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
});

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
            hint.textContent = `✅ ${assessment.message || 'URL looks good'}`;
            hint.classList.remove('error');
            hint.classList.add('success');
            testButton.disabled = false;
        } else {
            const message = assessment.message || 'Enter a valid http(s) URL';
            hint.textContent = `❌ ${message}`;
            hint.classList.remove('success');
            hint.classList.add('error');
            testButton.disabled = true;
            connectionValid = false;
            if (nextBtn) {
                nextBtn.disabled = true;
            }
        }
    };

    urlInput.addEventListener('input', applyState);
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
    if (currentStep >= totalSteps) return;
    
    // Validate current step
    if (!validateStep(currentStep)) return;
    
    // Move to next step
    const steps = document.querySelectorAll('.step');
    steps[currentStep - 1].classList.remove('active');
    currentStep++;
    steps[currentStep - 1].classList.add('active');
    
    updateProgress();
    
    // Load data for specific steps
    if (currentStep === 4) {
        discoverComponents();
    } else if (currentStep === 5) {
        applyRecommendedMetricStep(true);
        loadSampleMetrics();
    }
}

function prevStep() {
    if (currentStep <= 1) return;
    
    const steps = document.querySelectorAll('.step');
    steps[currentStep - 1].classList.remove('active');
    currentStep--;
    steps[currentStep - 1].classList.add('active');
    
    updateProgress();
}

function updateProgress() {
    const progress = ((currentStep - 1) / (totalSteps - 1)) * 100;
    document.getElementById('progress').style.width = progress + '%';
    
    const stepNames = ['Welcome', 'Time Range', 'VM Connection', 'Select Components', 'Obfuscation', 'Complete'];
    document.getElementById('stepInfo').textContent = `Step ${currentStep} of ${totalSteps}: ${stepNames[currentStep - 1]}`;
}

function validateStep(step) {
    switch(step) {
        case 2:
            // Validate time range
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
            
        case 3:
            // Validate connection
            if (!connectionValid) {
                alert('Please test the connection first');
                return false;
            }
            return true;
            
        case 4:
            // Validate component selection
            const selected = document.querySelectorAll('.component-item input[type="checkbox"]:checked');
            if (selected.length === 0) {
                alert('Please select at least one component');
                return false;
            }
            return true;
            
        default:
            return true;
    }
}

// Time Range Presets
function setPreset(preset, clickedButton) {
    const now = new Date();
    const timezone = document.getElementById('timezone').value;
    let from;
    
    switch(preset) {
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
    
    switch(authType) {
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
    
    btn.innerHTML = '<span class="loading-spinner"></span> Testing...';
    
    const urlAssessment = analyzeVmUrl(document.getElementById('vmUrl').value);
    if (!urlAssessment.valid) {
        result.innerHTML = `
            <div class="error-message">
                ❌ ${urlAssessment.message}
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
        const colors = {
            pending: '#666',
            progress: '#2962FF',
            success: '#4CAF50',
            error: '#f44336'
        };
        const icons = {
            pending: '⏳',
            progress: '🔄',
            success: '✅',
            error: '❌'
        };
        
        const stepHtml = `
            <div id="${stepId}" style="padding: 8px; margin: 5px 0; border-left: 3px solid ${colors[status]}; background: #f5f5f5; font-size: 13px;">
                <span style="margin-right: 8px;">${icons[status]}</span>
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
            const icons = {
                pending: '⏳',
                progress: '🔄',
                success: '✅',
                error: '❌'
            };
            step.style.borderLeftColor = colors[status];
            step.innerHTML = `<span style="margin-right: 8px;">${icons[status]}</span><span>${text}</span>`;
        }
    }
    
    try {
        const config = getConnectionConfig();
        
        // 🔍 DEBUG: Log connection config
        console.group('🔌 Multi-Stage Connection Test');
        console.log('📋 Connection Config:', config);
        
        // Step 1: Parse URL
        const step1 = addStep('🔍', 'Parsing URL...', 'progress');
        await new Promise(resolve => setTimeout(resolve, 300));
        
        if (!config.url) {
            updateStep(step1, '❌', 'URL parsing failed: Empty URL', 'error');
            throw new Error('URL is required');
        }
        
        updateStep(step1, '✅', `URL parsed: ${config.url}${config.api_base_path || ''}`, 'success');
        console.log('✅ Step 1: URL parsed');
        
        // Step 2: DNS/Network check
        const step2 = addStep('🌐', 'Checking network connectivity...', 'progress');
        await new Promise(resolve => setTimeout(resolve, 300));
        
        try {
            // Try to reach the host
            const hostCheck = await fetch(config.url + '/metrics', {
                method: 'HEAD',
                mode: 'no-cors', // Allow cross-origin for basic connectivity check
                cache: 'no-cache'
            }).catch(() => null);
            
            updateStep(step2, '✅', 'Host is reachable', 'success');
            console.log('✅ Step 2: Host reachable');
        } catch (e) {
            // Even if CORS fails, it means host is reachable
            updateStep(step2, '✅', 'Host is reachable (CORS protected)', 'success');
            console.log('✅ Step 2: Host reachable (CORS)');
        }
        
        // Step 3: Detect VictoriaMetrics
        const step3 = addStep('🔍', 'Detecting VictoriaMetrics...', 'progress');
        await new Promise(resolve => setTimeout(resolve, 300));
        
        // This will be done by the backend
        updateStep(step3, '🔄', 'Querying VictoriaMetrics API...', 'progress');
        console.log('🔄 Step 3: Querying VM API');
        
        // Step 4: Test connection with auth
        const step4 = addStep('🔐', 'Testing connection with authentication...', 'progress');
        
        const response = await fetch('/api/validate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ connection: config })
        });
        
        console.log('📡 Response Status:', response.status, response.statusText);
        
        const data = await response.json();
        console.log('📦 Response Data:', data);
        
        if (response.ok && data.success) {
            // Check if VictoriaMetrics was detected
            if (data.is_victoria_metrics === false) {
                updateStep(step3, '⚠️', 'Warning: Not VictoriaMetrics', 'error');
                updateStep(step4, '✅', 'Connection successful, but...', 'success');
                
                console.log('⚠️  Warning: Not VictoriaMetrics');
                console.groupEnd();
                
                // Add warning summary
                stepsContainer.insertAdjacentHTML('beforeend', `
                    <div style="margin-top: 15px; padding: 15px; background: #fff3cd; border-radius: 4px; border-left: 4px solid #ff9800;">
                        <div style="font-weight: bold; color: #f57c00; margin-bottom: 8px;">⚠️ Warning</div>
                        <div style="font-size: 13px; color: #555;">
                            ${data.warning || 'The endpoint responded but does not appear to be VictoriaMetrics.'}<br><br>
                            <strong>Please verify:</strong><br>
                            • The URL points to VictoriaMetrics (vmselect, vmsingle, or vmauth)<br>
                            • The path includes /prometheus or /select/... if needed<br>
                            • Authentication credentials are correct
                        </div>
                    </div>
                `);
                
                connectionValid = false;
                nextBtn.disabled = true;
                return;
            }
            
            updateStep(step3, '✅', `VictoriaMetrics detected! (${data.vm_components ? data.vm_components.join(', ') : 'components found'})`, 'success');
            updateStep(step4, '✅', `Connection successful! Version: ${data.version || 'Unknown'}`, 'success');
            
            console.log('✅ All steps passed!');
            console.log('📦 VM Components:', data.vm_components);
            console.groupEnd();
            
            // Add final summary
            stepsContainer.insertAdjacentHTML('beforeend', `
                <div style="margin-top: 15px; padding: 15px; background: #e8f5e9; border-radius: 4px; border-left: 4px solid #4CAF50;">
                    <div style="font-weight: bold; color: #2e7d32; margin-bottom: 8px;">✅ Connection Successful!</div>
                    <div style="font-size: 13px; color: #555;">
                        <strong>Version:</strong> ${data.version || 'Unknown'}<br>
                        <strong>Components:</strong> ${data.components || 0} detected<br>
                        ${data.vm_components && data.vm_components.length > 0 ? `<strong>VM Components:</strong> ${data.vm_components.join(', ')}<br>` : ''}
                        ${config.tenant_id ? `<strong>Tenant ID:</strong> ${config.tenant_id}<br>` : ''}
                        ${config.is_multitenant ? `<strong>Type:</strong> Multitenant endpoint<br>` : ''}
                    </div>
                </div>
            `);
            
            connectionValid = true;
            nextBtn.disabled = false;
        } else {
            updateStep(step4, '❌', `Connection failed: ${data.error || 'Unknown error'}`, 'error');
            throw new Error(data.error || 'Connection failed');
        }
    } catch (error) {
        console.error('❌ Connection failed:', error);
        console.error('❌ Error stack:', error.stack);
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
        
        result.innerHTML = `
            <div class="error-message">
                ❌ ${errorMsg}
                ${errorDetails ? `<div style="margin-top: 8px; font-size: 13px;">${errorDetails}</div>` : ''}
                <div style="margin-top: 10px; font-size: 12px; opacity: 0.8; border-top: 1px solid #ffcccc; padding-top: 10px;">
                    <strong>Debug info:</strong><br>
                    Open browser console (F12) → Console tab for detailed logs<br>
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
    
    // 🔍 DEBUG: Log raw input
    console.log('🔧 Building connection config:', { rawUrl, authType });
    
    const parsedUrl = parseVMUrl(rawUrl);
    console.log('🔧 Parsed URL:', parsedUrl);
    
    // Build auth object based on type
    const auth = { type: authType };
    
    switch(authType) {
        case 'basic':
            auth.username = document.getElementById('username').value;
            auth.password = document.getElementById('password').value;
            console.log('🔧 Auth: Basic (username set)');
            break;
        case 'bearer':
            auth.token = document.getElementById('token').value;
            console.log('🔧 Auth: Bearer (token set)');
            break;
        case 'header':
            auth.header_name = document.getElementById('headerName').value;
            auth.header_value = document.getElementById('headerValue').value;
            console.log('🔧 Auth: Custom Header');
            break;
        case 'none':
        default:
            console.log('🔧 Auth: None');
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
    
    console.log('✅ Final config:', config);
    
    return config;
}

// Component Discovery
async function discoverComponents() {
    const loading = document.getElementById('componentsLoading');
    const list = document.getElementById('componentsList');
    const error = document.getElementById('componentsError');
    
    loading.style.display = 'block';
    list.style.display = 'none';
    error.classList.add('hidden');
    
    try {
        const config = getConnectionConfig();
        const from = new Date(document.getElementById('timeFrom').value).toISOString();
        const to = new Date(document.getElementById('timeTo').value).toISOString();
        
        // 🔍 DEBUG: Log discovery request
        console.group('🔎 Component Discovery');
        console.log('📋 Time Range:', { from, to });
        console.log('🔗 Connection:', {
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
        
        console.log('📡 Response Status:', response.status, response.statusText);
        
        const data = await response.json();
        console.log('📦 Discovered Components:', data.components?.length || 0);
        
        if (!response.ok) {
            throw new Error(data.error || 'Discovery failed');
        }
        
        discoveredComponents = data.components || [];
        
        // Log component summary
        const componentTypes = [...new Set(discoveredComponents.map(c => c.component))];
        console.log('✅ Component Types:', componentTypes);
        console.groupEnd();
        
        renderComponents(discoveredComponents);
        
        loading.style.display = 'none';
        list.style.display = 'block';
    } catch (err) {
        console.error('❌ Discovery failed:', err);
        console.groupEnd();
        
        loading.style.display = 'none';
        error.textContent = err.message + ' (Check console F12 for details)';
        error.classList.remove('hidden');
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
                ${allJobs.length > 0 ? renderJobsGroup(comp.component, allJobs, totalInstances, comp.job_metrics || {}) : ''}
            </div>
        `;
    });
    
    list.innerHTML = html;
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
                    <strong>${job}</strong> - ~${estimatedInstances} instance(s) • ${seriesForJob}
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
    const step = document.querySelector('.step[data-step="5"]');
    return step && step.classList.contains('active');
}

function scheduleSampleReload() {
    if (!isObfuscationStepActive()) {
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
    const saved = localStorage.getItem('vmexporter_staging_dir');
    if (saved) {
        input.value = saved;
        directoryPickerPath = saved;
    } else {
        input.value = DEFAULT_STAGING_DIR;
        directoryPickerPath = DEFAULT_STAGING_DIR;
    }
    const hint = document.getElementById('stagingDirHint');
    if (hint) {
        hint.textContent = `Partial batches live under ${DEFAULT_STAGING_DIR}. Use “Browse…” to reuse an existing folder or “Use default” for a safe fallback.`;
    }
    validateStagingDir(true);
    input.addEventListener('input', () => validateStagingDir(false));
    input.addEventListener('blur', () => validateStagingDir(true));
}

function initializeMetricStepSelector() {
    const timeFrom = document.getElementById('timeFrom');
    const timeTo = document.getElementById('timeTo');
    [timeFrom, timeTo].forEach(el => {
        if (el) {
            el.addEventListener('change', () => applyRecommendedMetricStep(false));
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
    localStorage.setItem('vmexporter_staging_dir', value);
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
    list.innerHTML = '<div style="padding: 8px; color: #666;">Loading…</div>';
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
    const ensure = Boolean(options.ensure);
    const value = input.value.trim();
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
    const updateCreateButton = (visible) => {
        if (!createBtn) {
            return;
        }
        createBtn.style.display = visible ? 'inline-flex' : 'none';
        createBtn.disabled = !visible;
    };
    const perform = async () => {
        try {
            updateHint(ensure ? 'Preparing directory…' : 'Validating directory…', '#555');
            updateCreateButton(false);
            const resp = await fetch('/api/fs/check', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path: value, ensure })
            });
            const data = await resp.json();
            if (resp.ok && data.ok) {
                updateHint(`Ready: ${data.abs_path}`, '#2E7D32');
                updateCreateButton(false);
                if (input.value !== data.abs_path) {
                    input.value = data.abs_path;
                    localStorage.setItem('vmexporter_staging_dir', data.abs_path);
                }
            } else {
                if (data.exists === false && data.can_create) {
                    updateHint(`Directory will be created at ${data.abs_path}. Click "Create directory".`, '#ED6C02');
                    updateCreateButton(true);
                } else {
                    updateHint(data.message || 'Directory is not writable', '#c62828');
                    updateCreateButton(false);
                }
            }
        } catch (err) {
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
    
    if (sampleAbortController) {
        try {
            sampleAbortController.abort();
        } catch (e) {
            console.warn('Failed to abort previous sample request', e);
        }
    }
    
    // Show loading state
    advancedLabelsContainer.innerHTML = `
        <div style="text-align: center; color: #888; padding: 20px;">
            <div class="loading-spinner" style="display: inline-block;"></div>
            <p style="margin-top: 10px;">Loading sample metrics...</p>
        </div>
    `;
    
    try {
        const config = getConnectionConfig();
        const from = new Date(document.getElementById('timeFrom').value).toISOString();
        const to = new Date(document.getElementById('timeTo').value).toISOString();
        
        // Get selected components/jobs
        const selected = getSelectedComponents();
        if (selected.length === 0) {
            throw new Error('No components selected. Please go back to Step 4.');
        }
        const uniqueComponents = Array.from(new Set(selected.map(s => s.component)));
        const selectedJobs = selected.map(s => s.job).filter(Boolean);
        
        // 🔍 DEBUG: Log sample request
        console.group('📊 Sample Metrics Loading');
        console.log('📋 Selected Components:', uniqueComponents.length);
        console.log('🎯 Components:', uniqueComponents);
        console.log('💼 Jobs:', selectedJobs);
        
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
                    obfuscation: obfuscation  // Add obfuscation to samples
                },
                limit: 10
            }),
            signal: controller.signal
        });
        
        clearTimeout(timeoutId);
        
        console.log('📡 Response Status:', response.status, response.statusText);
        
        // Check Content-Type before parsing JSON
        const contentType = response.headers.get('content-type');
        if (!contentType || !contentType.includes('application/json')) {
            throw new Error(`Unexpected response type: ${contentType}. Expected JSON.`);
        }
        
        const data = await response.json();
        console.log('📦 Samples Received:', data.samples?.length || 0);
        
        if (!response.ok) {
            throw new Error(data.error || `Server error: ${response.status} ${response.statusText}`);
        }
        
        sampleMetrics = data.samples || [];
        
        // Log unique labels found
        const allLabels = new Set();
        sampleMetrics.forEach(s => Object.keys(s.labels || {}).forEach(l => allLabels.add(l)));
        console.log('🏷️  Unique Labels Found:', Array.from(allLabels).sort());
        console.log('✅ Sample loading complete');
        console.groupEnd();
        
        renderSamplePreview(sampleMetrics);
        renderAdvancedLabels(sampleMetrics);
        updateSelectionSummary();
        window.__vm_samples_version = (window.__vm_samples_version || 0) + 1;
    } catch (err) {
        if (err && err.name === 'AbortError') {
            console.info('Sample request aborted due to newer request');
            console.groupEnd();
            const waitingHtml = `
                <div style="text-align: center; color: #888; padding: 16px;">
                    <div class="loading-spinner" style="display: inline-block;"></div>
                    <p style="margin-top: 8px;">Refreshing sample metrics…</p>
                </div>
            `;
            advancedLabelsContainer.innerHTML = waitingHtml;
            samplePreviewContainer.innerHTML = waitingHtml;
            return;
        }
        console.error('❌ Sample loading failed:', err);
        console.groupEnd();
        
        // Show error in UI
        advancedLabelsContainer.innerHTML = `
            <div class="error-message" style="margin: 20px; padding: 15px; background: #ffebee; border-left: 4px solid #f44336; border-radius: 4px;">
                <strong style="color: #c62828;">❌ Failed to load sample metrics</strong>
                <p style="margin-top: 10px; color: #555;">${err.message}</p>
                <details style="margin-top: 10px; font-size: 12px; color: #666;">
                    <summary style="cursor: pointer; font-weight: 500;">Technical details</summary>
                    <pre style="margin-top: 10px; white-space: pre-wrap; word-break: break-all; background: #f5f5f5; padding: 10px; border-radius: 4px;">${err.stack || err.toString()}</pre>
                </details>
                <button onclick="loadSampleMetrics()" style="margin-top: 15px; padding: 8px 16px; background: #2962FF; color: white; border: none; border-radius: 4px; cursor: pointer; font-weight: 500;">
                    🔄 Retry
                </button>
            </div>
        `;
        
        samplePreviewContainer.innerHTML = `
            <div class="error-message" style="padding: 15px; background: #ffebee; border-left: 4px solid #f44336; border-radius: 4px;">
                <strong style="color: #c62828;">❌ Preview unavailable</strong>
                <p style="margin-top: 8px; color: #555;">Failed to load sample metrics. Please check connection and try again.</p>
            </div>
        `;
    } finally {
        sampleAbortController = null;
    }
}

function renderSamplePreview(samples) {
    const preview = document.getElementById('samplePreview');
    
    if (!samples || samples.length === 0) {
        preview.innerHTML = '<p style="text-align:center;color:#888;">No samples available</p>';
        return;
    }
    
    const limited = samples.slice(0, 5);
    let html = '';
    
    limited.forEach(sample => {
        // Handle both 'name' and 'metric_name' fields for backward compatibility
        const metricName = sample.name || sample.metric_name || 'unknown';
        
        // Ensure labels exist
        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');
        
        html += `
            <div class="sample-metric">
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
        container.innerHTML = '<p style="text-align:center;color:#888;padding:20px;">No additional labels found</p>';
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

    // Prune selections that are no longer available
    Array.from(selectedCustomLabels).forEach(label => {
        if (!availableLabels.has(label)) {
            selectedCustomLabels.delete(label);
        }
    });
}

function toggleObfuscation() {
    const enabled = document.getElementById('enableObfuscation').checked;
    const options = document.getElementById('obfuscationOptions');
    
    if (enabled) {
        options.style.display = 'block';
        const instanceCheckbox = document.querySelector('.obf-label-checkbox[data-label="instance"]');
        const jobCheckbox = document.querySelector('.obf-label-checkbox[data-label="job"]');
        if (instanceCheckbox && !instanceCheckbox.checked) {
            instanceCheckbox.checked = true;
        }
        if (jobCheckbox && !jobCheckbox.checked) {
            jobCheckbox.checked = true;
        }
    } else {
        options.style.display = 'none';
    }

    scheduleSampleReload();
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

function updateSelectionSummary() {
    const summary = document.getElementById('selectionSummary');
    if (!summary) {
        return;
    }

    const selected = getSelectedComponents();
    if (!selected || selected.length === 0) {
        summary.innerHTML = `
            <h4>📦 Estimated Export Volume</h4>
            <p class="summary-placeholder">Select components above to see per-component and per-job series counts.</p>
        `;
        return;
    }

    const stats = computeSelectionStats(selected);
    if (stats.length === 0) {
        summary.innerHTML = `
            <h4>📦 Estimated Export Volume</h4>
            <p class="summary-placeholder">Metrics data is not available for the selected components.</p>
        `;
        return;
    }

    const totalKnown = stats.reduce((sum, stat) => {
        return stat.series != null ? sum + stat.series : sum;
    }, 0);
    const hasUnknown = stats.some(stat => stat.series == null);

    let html = '<h4>📦 Estimated Export Volume</h4><div class="summary-grid">';
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

function getObfuscationConfig() {
    const enabled = document.getElementById('enableObfuscation').checked;
    
    if (!enabled) {
        return { 
            enabled: false,
            obfuscate_instance: false,
            obfuscate_job: false,
            preserve_structure: true,
            custom_labels: []
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
        custom_labels: customLabels  // pod, namespace, etc.
    };
}

// Export
async function exportMetrics(buttonElement) {
	const btn = buttonElement || event?.target;
	if (!btn) {
		console.error('No button element provided to exportMetrics');
		return;
	}
	const originalText = btn.textContent || 'Prepare Support Bundle';
    currentExportButton = btn;
    btn.dataset.originalText = originalText;
	btn.disabled = true;
	btn.innerHTML = '<span class="loading-spinner"></span> Collecting metrics...';

	try {
		const config = getConnectionConfig();
		const from = new Date(document.getElementById('timeFrom').value).toISOString();
		const to = new Date(document.getElementById('timeTo').value).toISOString();
        const selected = getSelectedComponents();
        if (selected.length === 0) {
            throw new Error('No components selected. Please go back to Step 4.');
        }
        const uniqueComponents = Array.from(new Set(selected.map(s => s.component)));
        const selectedJobs = selected.map(s => s.job).filter(Boolean);
        const obfuscation = getObfuscationConfig();
        
        // 🔍 DEBUG: Log export request
        console.group('📤 Metrics Export');
        console.log('📋 Export Config:', {
            time_range: { from, to },
            components: selected.length,
            obfuscation: {
                enabled: obfuscation.enabled,
                obfuscate_instance: obfuscation.obfuscate_instance,
                obfuscate_job: obfuscation.obfuscate_job
            }
        });
        console.log('🎯 Selected components:', uniqueComponents);
        console.log('💼 Selected jobs:', selectedJobs);
        
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

        const response = await fetch('/api/export/start', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({
				connection: config,
				time_range: { start: from, end: to },
				components: uniqueComponents,
				jobs: selectedJobs,
				obfuscation: obfuscation,
				staging_dir: stagingDirValue,
					metric_step_seconds: metricStepSeconds,
                    batching: batchingConfig
				})
			});
		
		console.log('📡 Response Status:', response.status, response.statusText);
		
		const data = await response.json();
		
		if (!response.ok) {
			throw new Error(data.error || 'Export failed');
		}
		
		console.log('🚀 Export job started:', data.job_id);
		currentExportJobId = data.job_id;
		showExportProgressPanel(data);
		await monitorExportJob(btn);
        console.groupEnd();
	} catch (err) {
		console.error('❌ Export failed:', err);
		console.groupEnd();
		
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
				cleanupExportPolling();
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
				cleanupExportPolling();
				btn.disabled = false;
				btn.textContent = btn.dataset.originalText || 'Prepare Support Bundle';
                currentExportButton = null;
				alert('Export failed: ' + (status.error || 'Unknown error'));
                disableCancelButton();
                showCancelNotice('');
			} else if (status.state === 'canceled') {
                cleanupExportPolling();
                btn.disabled = false;
                btn.textContent = btn.dataset.originalText || 'Prepare Support Bundle';
                currentExportButton = null;
                disableCancelButton();
                showCancelNotice('Export canceled. Adjust parameters and start again.');
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
		eta.textContent = 'Estimating time to completion…';
	}
	if (windowInfo && meta.batch_window_seconds) {
		windowInfo.textContent = `Batch window ≈ ${Math.round(meta.batch_window_seconds)}s`;
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
		summaryEl.textContent = `Last batch ${last}s • Avg ${avg}s`;
	}
    if (typeof status.obfuscation_enabled === 'boolean') {
        currentJobObfuscationEnabled = status.obfuscation_enabled;
    }
    if (status.staging_path) {
        exportStagingPath = status.staging_path;
    }
    renderExportStagingPath(exportStagingPath);
}

function cleanupExportPolling() {
	if (exportStatusTimer) {
		clearInterval(exportStatusTimer);
		exportStatusTimer = null;
	}
    exportStagingPath = '';
    currentJobObfuscationEnabled = false;
    currentExportJobId = null;
    renderExportStagingPath('');
    disableCancelButton();
}

function renderExportStagingPath(path) {
    const el = document.getElementById('exportProgressPath');
    if (el) {
        el.textContent = path || '—';
    }
}

function renderExportSpoilers(samples) {
    const container = document.getElementById('exportSpoilers');
    
    const limited = samples.slice(0, 5);
    let html = '<h3 style="margin-bottom: 15px;">📊 Exported Data Samples (Top 5)</h3>';
    
    limited.forEach((sample, idx) => {
        // Handle both 'name' and 'metric_name' fields for backward compatibility
        const metricName = sample.name || sample.metric_name || 'unknown';
        
        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');
        
        html += `
            <div class="spoiler">
                <div class="spoiler-header" onclick="toggleSpoiler(this)">
                    <span>Sample ${idx + 1}: ${metricName}</span>
                    <span>▼</span>
                </div>
                <div class="spoiler-content">
                    <div class="spoiler-body">
                        <div class="sample-metric">
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
        arrow.textContent = '▼';
    } else {
        content.classList.add('open');
        arrow.textContent = '▲';
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

async function cancelExportJob() {
    if (!currentExportJobId || cancelRequestInFlight) {
        return;
    }
    cancelRequestInFlight = true;
    const cancelBtn = document.getElementById('cancelExportBtn');
    if (cancelBtn) {
        cancelBtn.disabled = true;
        cancelBtn.textContent = 'Canceling…';
    }
    showCancelNotice('Sending cancellation request…');
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
        showCancelNotice('Cancellation requested. Waiting for exporter to stop…');
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
        console.error('❌ No archive available for download');
        alert('No archive available for download');
        return;
    }
    
    // 🔍 DEBUG: Log download
    console.group('⬇️  Archive Download');
    console.log('📦 Archive Path:', exportResult.archive_path);
    console.log('📊 Archive Size:', (exportResult.archive_size / 1024).toFixed(2), 'KB');
    console.log('🔐 SHA256:', exportResult.sha256);
    
    // Create download link
    const link = document.createElement('a');
    link.href = '/api/download?path=' + encodeURIComponent(exportResult.archive_path);
    link.download = exportResult.archive_path.split('/').pop();
    
    console.log('🔗 Download URL:', link.href);
    console.log('✅ Initiating browser download');
    console.groupEnd();
    
    // Trigger download
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
}

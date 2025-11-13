// VMExporter Frontend - Enhanced UX/UI
// State
let currentStep = 1;
const totalSteps = 6;
let connectionValid = false;
let discoveredComponents = [];
let sampleMetrics = [];
let exportResult = null;

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    // Set default time range (last 1 hour)
    setPreset('1h');
    
    // Initialize datetime-local inputs
    initializeDateTimePickers();
});

// DateTime Picker initialization
function initializeDateTimePickers() {
    const now = new Date();
    const oneHourAgo = new Date(now.getTime() - 60 * 60 * 1000);
    
    document.getElementById('timeTo').value = formatDateTimeLocal(now);
    document.getElementById('timeFrom').value = formatDateTimeLocal(oneHourAgo);
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
    
    btn.innerHTML = '<span class="loading-spinner"></span> Testing...';
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
    try {
        // Remove trailing slash
        rawUrl = rawUrl.trim().replace(/\/$/, '');
        
        const url = new URL(rawUrl);
        const path = url.pathname;
        
        // Extract base URL (protocol + host)
        const baseUrl = `${url.protocol}//${url.host}`;
        
        // Detect URL format
        let apiBasePath = '';
        let tenantId = null;
        let isMultitenant = false;
        
        // Check for /select/<tenant>/prometheus or /select/multitenant patterns
        const selectMatch = path.match(/^(\/select\/(\d+|multitenant))(\/prometheus)?/);
        if (selectMatch) {
            const tenant = selectMatch[2];
            if (tenant === 'multitenant') {
                isMultitenant = true;
                apiBasePath = '/select/multitenant/prometheus';
            } else {
                tenantId = tenant;
                apiBasePath = `/select/${tenant}/prometheus`;
            }
        } 
        // Check for simple tenant ID at path (e.g., /1011)
        else if (path.match(/^\/\d+$/)) {
            tenantId = path.substring(1);
            apiBasePath = path + '/prometheus';
        }
        // Check if path contains /prometheus or other known endpoints
        else if (path.includes('/prometheus')) {
            apiBasePath = path;
        }
        // Otherwise, use path as-is or empty if just base URL
        else if (path && path !== '/') {
            apiBasePath = path + '/prometheus';
        } else {
            apiBasePath = '/prometheus';
        }
        
        return {
            baseUrl: baseUrl,
            apiBasePath: apiBasePath,
            tenantId: tenantId,
            isMultitenant: isMultitenant,
            fullApiUrl: baseUrl + apiBasePath
        };
    } catch (e) {
        // If URL parsing fails, return as-is
        return {
            baseUrl: rawUrl,
            apiBasePath: '/prometheus',
            tenantId: null,
            isMultitenant: false,
            fullApiUrl: rawUrl + '/prometheus'
        };
    }
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
                    Jobs: ${allJobs.join(', ')} | Instances: ${totalInstances}
                </div>
                ${allJobs.length > 1 ? renderJobsGroup(comp.component, allJobs, totalInstances) : ''}
            </div>
        `;
    });
    
    list.innerHTML = html;
}

function renderJobsGroup(componentType, jobs, totalInstances) {
    let html = '<div class="jobs-group">';
    
    // jobs is an array of job names (strings)
    jobs.forEach(job => {
        // Estimate instances per job (divide equally)
        const estimatedInstances = Math.ceil(totalInstances / jobs.length);
        
        html += `
            <div class="job-item">
                <label onclick="event.stopPropagation();">
                    <input type="checkbox" 
                           data-component="${componentType}" 
                           data-job="${job}"
                           onchange="handleJobCheck(this)">
                    <strong>${job}</strong> - ~${estimatedInstances} instance(s)
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
}

// Sample Metrics Loading
async function loadSampleMetrics() {
    const advancedLabelsContainer = document.getElementById('advancedLabels');
    const samplePreviewContainer = document.getElementById('samplePreview');
    
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
        
        // 🔍 DEBUG: Log sample request
        console.group('📊 Sample Metrics Loading');
        console.log('📋 Selected Components:', selected.length);
        console.log('🎯 Components:', selected.map(s => s.component));
        console.log('💼 Jobs:', selected.map(s => s.job).filter(Boolean));
        
        // Add timeout (30 seconds)
        const controller = new AbortController();
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
                    components: selected.map(s => s.component),
                    jobs: selected.map(s => s.job).filter(Boolean),
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
    } catch (err) {
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
        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');
        
        html += `
            <div class="sample-metric">
                <div class="metric-name">${sample.name}</div>
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
    
    let html = '';
    filteredLabels.forEach(label => {
        const sample = labelSamples[label] || 'example_value';
        html += `
            <div class="label-item">
                <label>
                    <input type="checkbox" class="obf-label-checkbox" data-label="${label}">
                    <strong>${label}</strong>
                    <span class="label-sample">(e.g., ${sample})</span>
                </label>
            </div>
        `;
    });
    
    container.innerHTML = html;
}

function toggleObfuscation() {
    const enabled = document.getElementById('enableObfuscation').checked;
    const options = document.getElementById('obfuscationOptions');
    
    if (enabled) {
        options.style.display = 'block';
        // Auto-enable instance and job obfuscation when enabling obfuscation
        const instanceCheckbox = document.querySelector('[data-label="instance"]');
        const jobCheckbox = document.querySelector('[data-label="job"]');
        if (instanceCheckbox) instanceCheckbox.checked = true;
        if (jobCheckbox) jobCheckbox.checked = true;
    } else {
        options.style.display = 'none';
    }
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
    const selectedLabels = [];
    document.querySelectorAll('.obf-label-checkbox:checked').forEach(cb => {
        selectedLabels.push(cb.dataset.label);
    });
    
    // Separate standard labels (instance, job) from custom labels (pod, namespace, etc.)
    const customLabels = selectedLabels.filter(label => 
        label !== 'instance' && label !== 'job'
    );
    
    // Map labels to backend format
    return {
        enabled: true,
        obfuscate_instance: selectedLabels.includes('instance'),
        obfuscate_job: selectedLabels.includes('job'),
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
    const originalText = btn.textContent;
    btn.disabled = true;
    btn.innerHTML = '<span class="loading-spinner"></span> Exporting...';
    
    try {
        const config = getConnectionConfig();
        const from = new Date(document.getElementById('timeFrom').value).toISOString();
        const to = new Date(document.getElementById('timeTo').value).toISOString();
        const selected = getSelectedComponents();
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
        console.log('🎯 Selected:', selected);
        
        const response = await fetch('/api/export', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                connection: config,
                time_range: { start: from, end: to },
                components: selected.map(s => s.component),
                jobs: selected.map(s => s.job).filter(Boolean),
                obfuscation: obfuscation
            })
        });
        
        console.log('📡 Response Status:', response.status, response.statusText);
        
        const data = await response.json();
        
        if (!response.ok) {
            throw new Error(data.error || 'Export failed');
        }
        
        console.log('📦 Export Result:', {
            export_id: data.export_id,
            metrics_count: data.metrics_count,
            archive_size_kb: (data.archive_size / 1024).toFixed(2),
            obfuscation_applied: data.obfuscation_applied,
            sample_data_count: data.sample_data?.length || 0
        });
        console.log('✅ Export complete');
        console.groupEnd();
        
        exportResult = data;
        showExportResult(data);
        nextStep();
    } catch (err) {
        console.error('❌ Export failed:', err);
        console.groupEnd();
        
        alert('Export failed: ' + err.message + '\n\nCheck browser console (F12) for details');
        btn.disabled = false;
        btn.textContent = originalText;
    }
}

function showExportResult(data) {
    document.getElementById('exportId').textContent = data.export_id || 'N/A';
    document.getElementById('metricsCount').textContent = (data.metrics_count || 0).toLocaleString();
    document.getElementById('archiveSize').textContent = ((data.archive_size || 0) / 1024).toFixed(2);
    document.getElementById('archiveSha256').textContent = data.sha256 || 'N/A';
    
    // Render spoilers with sample data
    if (data.sample_data && data.sample_data.length > 0) {
        renderExportSpoilers(data.sample_data);
    }
}

function renderExportSpoilers(samples) {
    const container = document.getElementById('exportSpoilers');
    
    const limited = samples.slice(0, 5);
    let html = '<h3 style="margin-bottom: 15px;">📊 Exported Data Samples (Top 5)</h3>';
    
    limited.forEach((sample, idx) => {
        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');
        
        html += `
            <div class="spoiler">
                <div class="spoiler-header" onclick="toggleSpoiler(this)">
                    <span>Sample ${idx + 1}: ${sample.name}</span>
                    <span>▼</span>
                </div>
                <div class="spoiler-content">
                    <div class="spoiler-body">
                        <div class="sample-metric">
                            <div class="metric-name">${sample.name}</div>
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


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

function formatDateTimeLocal(date) {
    // Format: YYYY-MM-DDTHH:mm
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    const hours = String(date.getHours()).padStart(2, '0');
    const minutes = String(date.getMinutes()).padStart(2, '0');
    
    return `${year}-${month}-${day}T${hours}:${minutes}`;
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
function setPreset(preset) {
    const now = new Date();
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
    
    document.getElementById('timeFrom').value = formatDateTimeLocal(from);
    document.getElementById('timeTo').value = formatDateTimeLocal(now);
    
    // Update button states
    document.querySelectorAll('.preset-btn').forEach(btn => btn.classList.remove('active'));
    event.target.classList.add('active');
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

// Connection Test
async function testConnection() {
    const btn = document.getElementById('testBtnText');
    const result = document.getElementById('connectionResult');
    const nextBtn = document.getElementById('step3Next');
    
    btn.innerHTML = '<span class="loading-spinner"></span> Testing...';
    result.innerHTML = '';
    
    try {
        const config = getConnectionConfig();
        
        const response = await fetch('/api/validate', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ connection: config })
        });
        
        const data = await response.json();
        
        if (response.ok && data.success) {
            result.innerHTML = `
                <div class="success-box">
                    [OK] Connection successful!
                    <div style="margin-top: 10px; font-size: 13px;">
                        Version: ${data.version || 'Unknown'}<br>
                        Components: ${data.components || 0} detected
                    </div>
                </div>
            `;
            connectionValid = true;
            nextBtn.disabled = false;
        } else {
            throw new Error(data.error || 'Connection failed');
        }
    } catch (error) {
        result.innerHTML = `
            <div class="error-message">
                [FAIL] ${error.message}
            </div>
        `;
        connectionValid = false;
        nextBtn.disabled = true;
    } finally {
        btn.textContent = 'Test Connection';
    }
}

function getConnectionConfig() {
    const authType = document.getElementById('authType').value;
    const config = {
        url: document.getElementById('vmUrl').value,
        auth_type: authType
    };
    
    switch(authType) {
        case 'basic':
            config.username = document.getElementById('username').value;
            config.password = document.getElementById('password').value;
            break;
        case 'bearer':
            config.token = document.getElementById('token').value;
            break;
        case 'header':
            config.header_name = document.getElementById('headerName').value;
            config.header_value = document.getElementById('headerValue').value;
            break;
    }
    
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
        
        const response = await fetch('/api/discover', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                connection: config,
                time_range: { start: from, end: to }
            })
        });
        
        const data = await response.json();
        
        if (!response.ok) {
            throw new Error(data.error || 'Discovery failed');
        }
        
        discoveredComponents = data.components || [];
        renderComponents(discoveredComponents);
        
        loading.style.display = 'none';
        list.style.display = 'block';
    } catch (err) {
        loading.style.display = 'none';
        error.textContent = err.message;
        error.classList.remove('hidden');
    }
}

function renderComponents(components) {
    const list = document.getElementById('componentsList');
    
    if (components.length === 0) {
        list.innerHTML = '<p style="text-align:center;color:#888;">No components found</p>';
        return;
    }
    
    // Group by component type
    const grouped = {};
    components.forEach(comp => {
        if (!grouped[comp.component]) {
            grouped[comp.component] = [];
        }
        grouped[comp.component].push(comp);
    });
    
    let html = '';
    Object.keys(grouped).sort().forEach(componentType => {
        const items = grouped[componentType];
        const totalInstances = items.reduce((sum, item) => sum + (item.instance_count || 0), 0);
        
        html += `
            <div class="component-item" onclick="toggleComponent(this)">
                <div class="component-header">
                    <input type="checkbox" 
                           data-component="${componentType}" 
                           onclick="event.stopPropagation();" 
                           onchange="handleComponentCheck(this)">
                    <strong>${componentType}</strong>
                </div>
                <div class="component-details">
                    Jobs: ${items.map(i => i.job).join(', ')} | Instances: ${totalInstances}
                </div>
                ${items.length > 1 ? renderJobsGroup(componentType, items) : ''}
            </div>
        `;
    });
    
    list.innerHTML = html;
}

function renderJobsGroup(componentType, items) {
    let html = '<div class="jobs-group">';
    items.forEach(item => {
        html += `
            <div class="job-item">
                <label onclick="event.stopPropagation();">
                    <input type="checkbox" 
                           data-component="${componentType}" 
                           data-job="${item.job}"
                           onchange="handleJobCheck(this)">
                    <strong>${item.job}</strong> - ${item.instance_count} instance(s)
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
    try {
        const config = getConnectionConfig();
        const from = new Date(document.getElementById('timeFrom').value).toISOString();
        const to = new Date(document.getElementById('timeTo').value).toISOString();
        
        // Get selected components/jobs
        const selected = getSelectedComponents();
        
        const response = await fetch('/api/sample', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                connection: config,
                time_range: { start: from, end: to },
                components: selected.map(s => s.component),
                jobs: selected.map(s => s.job).filter(Boolean)
            })
        });
        
        const data = await response.json();
        
        if (!response.ok) {
            throw new Error(data.error || 'Failed to load samples');
        }
        
        sampleMetrics = data.samples || [];
        renderSamplePreview(sampleMetrics);
        renderAdvancedLabels(sampleMetrics);
    } catch (err) {
        console.error('Failed to load samples:', err);
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
        return { enabled: false, labels: [] };
    }
    
    const labels = [];
    document.querySelectorAll('.obf-label-checkbox:checked').forEach(cb => {
        labels.push(cb.dataset.label);
    });
    
    return {
        enabled: true,
        labels: labels
    };
}

// Export
async function exportMetrics() {
    const btn = event.target;
    const originalText = btn.textContent;
    btn.disabled = true;
    btn.innerHTML = '<span class="loading-spinner"></span> Exporting...';
    
    try {
        const config = getConnectionConfig();
        const from = new Date(document.getElementById('timeFrom').value).toISOString();
        const to = new Date(document.getElementById('timeTo').value).toISOString();
        const selected = getSelectedComponents();
        const obfuscation = getObfuscationConfig();
        
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
        
        const data = await response.json();
        
        if (!response.ok) {
            throw new Error(data.error || 'Export failed');
        }
        
        exportResult = data;
        showExportResult(data);
        nextStep();
    } catch (err) {
        alert('Export failed: ' + err.message);
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
    let html = '<h3 style="margin-bottom: 15px;">[STATS] Exported Data Samples (Top 5)</h3>';
    
    limited.forEach((sample, idx) => {
        const labels = Object.entries(sample.labels || {})
            .map(([k, v]) => `${k}="${v}"`)
            .join(', ');
        
        html += `
            <div class="spoiler">
                <div class="spoiler-header" onclick="toggleSpoiler(this)">
                    <span>Sample ${idx + 1}: ${sample.name}</span>
                    <span>v</span>
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
        arrow.textContent = 'v';
    } else {
        content.classList.add('open');
        arrow.textContent = '^';
    }
}

// Download
function downloadArchive() {
    if (!exportResult || !exportResult.archive_path) {
        alert('No archive available for download');
        return;
    }
    
    // Create download link
    const link = document.createElement('a');
    link.href = '/api/download?path=' + encodeURIComponent(exportResult.archive_path);
    link.download = exportResult.archive_path.split('/').pop();
    
    // Trigger download
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
}


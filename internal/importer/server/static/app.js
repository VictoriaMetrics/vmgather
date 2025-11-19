const dropZone = document.getElementById('dropZone');
const bundleInput = document.getElementById('bundleFile');
const uploadBtn = document.getElementById('uploadBtn');
const statusPanel = document.getElementById('statusPanel');

['dragenter', 'dragover'].forEach(evt => dropZone.addEventListener(evt, e => {
    e.preventDefault();
    e.stopPropagation();
    dropZone.classList.add('dragover');
}));
['dragleave', 'drop'].forEach(evt => dropZone.addEventListener(evt, e => {
    e.preventDefault();
    e.stopPropagation();
    dropZone.classList.remove('dragover');
}));

dropZone.addEventListener('click', () => bundleInput.click());
dropZone.addEventListener('drop', event => {
    const files = event.dataTransfer.files;
    if (files.length) {
        bundleInput.files = files;
        dropZone.querySelector('strong').textContent = files[0].name;
    }
});

function resetForm() {
    bundleInput.value = '';
    dropZone.querySelector('strong').textContent = 'Drop file here';
    document.getElementById('endpoint').value = '';
    document.getElementById('tenantId').value = '';
    document.getElementById('username').value = '';
    document.getElementById('password').value = '';
    document.getElementById('skipTls').checked = false;
    statusPanel.style.display = 'none';
}

async function uploadBundle() {
    const file = bundleInput.files[0];
    if (!file) {
        showStatus('Please select .jsonl or .zip bundle first.', true);
        return;
    }
    const config = {
        endpoint: document.getElementById('endpoint').value.trim(),
        tenant_id: document.getElementById('tenantId').value.trim(),
        username: document.getElementById('username').value.trim(),
        password: document.getElementById('password').value,
        skip_tls_verify: document.getElementById('skipTls').checked,
    };
    if (!config.endpoint) {
        showStatus('Endpoint URL is required.', true);
        return;
    }

    uploadBtn.disabled = true;
    uploadBtn.textContent = 'Uploading…';
    showStatus('Uploading bundle…', false);

    try {
        const form = new FormData();
        form.append('bundle', file);
        form.append('config', JSON.stringify(config));

        const resp = await fetch('/api/upload', {
            method: 'POST',
            body: form,
        });
        const data = await resp.json();
        if (!resp.ok) {
            throw new Error(data.error || 'Import failed');
        }
        showStatus(`Uploaded ${formatBytes(data.bytes_sent)} to ${data.remote_path}. Response: ${data.message || 'OK'}`, false, true);
    } catch (err) {
        showStatus(err.message, true);
    } finally {
        uploadBtn.disabled = false;
        uploadBtn.textContent = 'Send bundle';
    }
}

function showStatus(text, isError = false, success = false) {
    statusPanel.style.display = 'block';
    statusPanel.textContent = text;
    statusPanel.classList.remove('success', 'error');
    if (isError) {
        statusPanel.classList.add('error');
    } else if (success) {
        statusPanel.classList.add('success');
    }
}

function formatBytes(bytes) {
    if (!bytes) return '0B';
    const units = ['B', 'KB', 'MB', 'GB'];
    const idx = Math.floor(Math.log(bytes) / Math.log(1024));
    return `${(bytes / Math.pow(1024, idx)).toFixed(1)} ${units[idx]}`;
}

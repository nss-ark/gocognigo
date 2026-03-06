// === GoCognigo — File Upload ===

// Upload a single file with progress tracking using XHR
function uploadFileWithProgress(file) {
    return new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        const formData = new FormData();
        formData.append('files', file);
        formData.append('project_id', activeProjectId);

        xhr.upload.addEventListener('progress', (e) => {
            if (e.lengthComputable) {
                const pct = Math.round((e.loaded / e.total) * 100);
                updateFileProgressUI(file.name, pct);
            }
        });

        xhr.addEventListener('load', () => {
            if (xhr.status >= 200 && xhr.status < 300) {
                resolve();
            } else {
                try {
                    const err = JSON.parse(xhr.responseText);
                    reject(new Error(err.error || 'Upload failed'));
                } catch {
                    reject(new Error('Upload failed'));
                }
            }
        });

        xhr.addEventListener('error', () => reject(new Error('Network error')));
        xhr.addEventListener('abort', () => reject(new Error('Upload cancelled')));

        xhr.open('POST', `${API_BASE}/api/upload`);
        xhr.send(formData);
    });
}

async function handleFileDrop(fileList) {
    if (!activeProjectId) {
        alert('Create or select a chat first.');
        return;
    }

    const validFiles = Array.from(fileList).filter(f => {
        const ext = f.name.toLowerCase().split('.').pop();
        return ext === 'pdf' || ext === 'docx';
    });

    if (validFiles.length === 0) {
        alert('Only PDF and DOCX files are supported.');
        return;
    }

    // Upload files one at a time for reliability and progress feedback
    const processBtn = document.getElementById('processBtn');
    processBtn.classList.add('hidden');

    for (let i = 0; i < validFiles.length; i++) {
        const file = validFiles[i];

        // Show uploading indicator with progress bar in file list
        addFileToListUI(file.name, file.size, 'uploading', `${i + 1}/${validFiles.length}`);

        try {
            await uploadFileWithProgress(file);
            updateFileStatusUI(file.name, 'done', '\u2713');
        } catch (e) {
            updateFileStatusUI(file.name, 'error', '\u2717 ' + e.message);
        }
    }

    // Refresh full file list from server and session data
    await loadUploadedFiles();
    try {
        const chatsRes = await fetch(`${API_BASE}/api/chats`);
        projects = await chatsRes.json();
        renderSidebar();
    } catch (e) { /* ignore */ }
}

function addFileToListUI(name, size, status, counter) {
    const container = document.getElementById('fileList');
    const ext = name.toLowerCase().split('.').pop();
    const sizeStr = formatFileSize(size);
    const statusText = status === 'uploading' ? '0%' : status === 'error' ? '\u2717' : '\u2713';
    const statusClass = status === 'uploading' ? 'uploading' : status === 'error' ? 'error' : '';

    const div = document.createElement('div');
    div.className = 'file-item';
    div.id = `file-${name.replace(/[^a-zA-Z0-9]/g, '_')}`;
    div.innerHTML = `
        <div class="file-icon ${ext}">${ext}</div>
        <span class="file-name">${escapeHtml(name)}</span>
        <span class="file-size">${sizeStr}</span>
        <span class="file-status ${statusClass}">${statusText}</span>
        <button class="file-remove" onclick="removeFile('${escapeHtml(name)}')" title="Remove">\u00d7</button>
        ${status === 'uploading' ? '<div class="file-progress-bar"><div class="file-progress-fill"></div></div>' : ''}
    `;
    container.appendChild(div);
}

function updateFileProgressUI(name, percent) {
    const id = `file-${name.replace(/[^a-zA-Z0-9]/g, '_')}`;
    const el = document.getElementById(id);
    if (!el) return;

    const fill = el.querySelector('.file-progress-fill');
    if (fill) fill.style.width = `${percent}%`;

    const statusEl = el.querySelector('.file-status');
    if (statusEl) statusEl.textContent = `${percent}%`;
}

function updateFileStatusUI(name, status, text) {
    const id = `file-${name.replace(/[^a-zA-Z0-9]/g, '_')}`;
    const el = document.getElementById(id);
    if (!el) return;

    // Remove progress bar on completion
    const bar = el.querySelector('.file-progress-bar');
    if (bar) bar.remove();

    const statusEl = el.querySelector('.file-status');
    if (statusEl) {
        statusEl.textContent = text;
        statusEl.className = 'file-status' + (status === 'error' ? ' error' : '');
    }
}

async function loadUploadedFiles() {
    if (!activeProjectId) return;
    try {
        const res = await fetch(`${API_BASE}/api/files?project_id=${activeProjectId}`);
        uploadedFiles = await res.json();
        if (!uploadedFiles) uploadedFiles = [];
        renderFileList();
    } catch (e) {
        uploadedFiles = [];
        renderFileList();
    }
}

function renderFileList() {
    const container = document.getElementById('fileList');
    const processBtn = document.getElementById('processBtn');

    if (uploadedFiles.length === 0) {
        container.innerHTML = '';
        processBtn.classList.add('hidden');
        return;
    }

    processBtn.classList.remove('hidden');

    container.innerHTML = uploadedFiles.map(f => {
        const ext = f.name.toLowerCase().split('.').pop();
        const sizeStr = formatFileSize(f.size);
        const safeName = escapeHtml(f.name).replace(/'/g, "\\'");
        return `<div class="file-item">
            <div class="file-icon ${ext}">${ext}</div>
            <span class="file-name">${escapeHtml(f.name)}</span>
            <span class="file-size">${sizeStr}</span>
            <span class="file-status">\u2713</span>
            <button class="file-remove" onclick="removeFile('${safeName}')" title="Remove">\u00d7</button>
        </div>`;
    }).join('');
}

async function removeFile(name) {
    if (!activeProjectId) return;

    try {
        const res = await fetch(`${API_BASE}/api/files/delete`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: activeProjectId, name: name })
        });

        if (!res.ok) {
            const err = await res.json().catch(() => ({ error: 'Delete failed' }));
            alert('Failed to remove file: ' + (err.error || 'Unknown error'));
            return;
        }

        // Refresh file list from server
        await loadUploadedFiles();

        // Refresh stats (document count, chunk count) in header
        await loadStats();

        // Re-render indexed files panel
        renderIndexedFiles();

        // Refresh projects to update file count in sidebar
        const projRes = await fetch(`${API_BASE}/api/chats`);
        projects = await projRes.json();
        renderSidebar();
    } catch (e) {
        console.error('Failed to remove file', e);
        alert('Error removing file: ' + e.message);
    }
}

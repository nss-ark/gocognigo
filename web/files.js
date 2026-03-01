// === GoCognigo — File Upload ===

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

        // Show uploading indicator in file list
        addFileToListUI(file.name, file.size, 'uploading', `${i + 1}/${validFiles.length}`);

        const formData = new FormData();
        formData.append('files', file);
        formData.append('project_id', activeProjectId);

        try {
            const res = await fetch(`${API_BASE}/api/upload`, {
                method: 'POST',
                body: formData
            });

            if (!res.ok) {
                const err = await res.json().catch(() => ({ error: 'Upload failed' }));
                updateFileStatusUI(file.name, 'error', err.error || 'Failed');
                continue;
            }

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
    const statusText = status === 'uploading' ? `\u2B06 ${counter}` : status === 'error' ? '\u2717' : '\u2713';
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
    `;
    container.appendChild(div);
}

function updateFileStatusUI(name, status, text) {
    const id = `file-${name.replace(/[^a-zA-Z0-9]/g, '_')}`;
    const el = document.getElementById(id);
    if (!el) return;
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

        // Refresh projects to update file count in sidebar
        const projRes = await fetch(`${API_BASE}/api/chats`);
        projects = await projRes.json();
        renderSidebar();
    } catch (e) {
        console.error('Failed to remove file', e);
        alert('Error removing file: ' + e.message);
    }
}

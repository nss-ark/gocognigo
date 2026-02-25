// === GoCognigo \u2014 Client Application ===

const API_BASE = '';

const TEST_QUESTIONS = [
    "What are the revenue figures for Meta for Q1, Q2 and Q3?",
    "What was KFIN's revenue in 2021?",
    "What metrics helped CCI determine if the combination would be anticompetitive?",
    "What was the bench in the Eastman Kodak Case?",
    "How many SCOTUS cases are in the set? Name them.",
    "What is the governing law in the NVCA IRA?",
    "If Pristine were to acquire an indian company that had turnover of 1Cr and no assets, would it have to notify the deal to the CCI?"
];

// ===== State =====
let currentPhase = 'upload';    // 'upload' | 'processing' | 'chat'
let activeProjectId = null;
let activeConversationId = null;
let projects = [];
let conversations = [];
let uploadedFiles = [];
let convHasBeenNamed = false;

let currentMode = 'single';
let currentProvider = 'openai';
let currentModel = '';
let availableProviders = [];
let providerModels = {};
let timerInterval = null;
let ingestPollInterval = null;

// ===== Init =====
document.addEventListener('DOMContentLoaded', () => {
    loadProviders();

    // Sidebar toggle (mobile)
    document.getElementById('sidebarToggle').addEventListener('click', () => {
        document.getElementById('sidebar').classList.toggle('open');
    });

    // New project
    document.getElementById('newChatBtn').addEventListener('click', createProject);

    // Upload zone events
    const uploadZone = document.getElementById('uploadZone');
    const fileInput = document.getElementById('fileInput');

    uploadZone.addEventListener('click', () => fileInput.click());
    uploadZone.addEventListener('dragover', (e) => {
        e.preventDefault();
        uploadZone.classList.add('drag-over');
    });
    uploadZone.addEventListener('dragleave', () => {
        uploadZone.classList.remove('drag-over');
    });
    uploadZone.addEventListener('drop', (e) => {
        e.preventDefault();
        uploadZone.classList.remove('drag-over');
        handleFileDrop(e.dataTransfer.files);
    });
    fileInput.addEventListener('change', (e) => {
        handleFileDrop(e.target.files);
        fileInput.value = '';
    });

    // Process button
    document.getElementById('processBtn').addEventListener('click', startIngestion);

    // Chat action buttons
    document.getElementById('uploadMoreBtn').addEventListener('click', () => showPhase('upload'));
    document.getElementById('resetBtn').addEventListener('click', resetChat);

    // Indexed files toggle
    document.getElementById('indexedFilesToggle').addEventListener('click', () => {
        const panel = document.getElementById('indexedFilesPanel');
        const list = document.getElementById('indexedFilesList');
        panel.classList.toggle('open');
        list.classList.toggle('hidden');
    });

    // Query input enter key
    document.getElementById('queryInput').addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            submitQuery();
        }
    });

    // Provider toggle
    document.querySelectorAll('.provider-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            const provider = btn.dataset.provider;
            if (!availableProviders.includes(provider)) {
                alert(`${provider} is not configured. Add the API key to .env and restart the server.`);
                return;
            }
            setProvider(provider);
        });
    });

    // Model selector
    document.getElementById('modelSelect').addEventListener('change', (e) => {
        currentModel = e.target.value;
    });

    // Mode switching
    document.getElementById('singleModeBtn').addEventListener('click', () => switchMode('single'));
    document.getElementById('batchModeBtn').addEventListener('click', () => switchMode('batch'));

    // Settings panel
    document.getElementById('settingsBtn').addEventListener('click', (e) => {
        e.stopPropagation();
        const dd = document.getElementById('settingsDropdown');
        const wasHidden = dd.classList.contains('hidden');
        dd.classList.toggle('hidden');
        if (wasHidden) loadSettings();
    });
    document.getElementById('settingsClose').addEventListener('click', () => {
        document.getElementById('settingsDropdown').classList.add('hidden');
    });
    document.getElementById('settingsSaveBtn').addEventListener('click', saveSettings);
    // OCR provider toggle to show/hide Sarvam key
    document.getElementById('settingsOCR').addEventListener('change', (e) => {
        document.getElementById('sarvamKeyGroup').style.display = e.target.value === 'sarvam' ? '' : 'none';
    });
    // Close settings when clicking outside
    document.addEventListener('click', (e) => {
        const wrap = document.querySelector('.settings-wrap');
        if (wrap && !wrap.contains(e.target)) {
            document.getElementById('settingsDropdown').classList.add('hidden');
        }
    });

    // Load projects and initialise
    loadProjects();
});

// ===== Project Management =====

async function loadProjects() {
    try {
        const res = await fetch(`${API_BASE}/api/chats`);
        projects = await res.json();
        if (!projects) projects = [];
    } catch (e) {
        projects = [];
    }

    renderSidebar();

    if (projects.length === 0) {
        showEmptyState();
    } else {
        hideEmptyState();
        const latest = projects[projects.length - 1];
        await activateProject(latest.id);
    }
}

function showEmptyState() {
    document.getElementById('emptyState').classList.remove('hidden');
    document.getElementById('uploadPhase').classList.add('hidden');
    document.getElementById('processingPhase').classList.add('hidden');
    document.getElementById('chatPhase').classList.add('hidden');
}

function hideEmptyState() {
    document.getElementById('emptyState').classList.add('hidden');
}

async function createProject() {
    try {
        const res = await fetch(`${API_BASE}/api/chats`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: '' })
        });
        const proj = await res.json();
        activeProjectId = proj.id;
        activeConversationId = null;
        conversations = [];
        projects.push(proj);
        renderSidebar();
        uploadedFiles = [];
        renderFileList();
        convHasBeenNamed = false;
        hideEmptyState();
        showPhase('upload');
        loadStats();
    } catch (e) {
        console.error('Failed to create project', e);
    }
}

async function activateProject(id) {
    if (ingestPollInterval) {
        clearInterval(ingestPollInterval);
        ingestPollInterval = null;
    }

    try {
        const res = await fetch(`${API_BASE}/api/chats/activate`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ chat_id: id })
        });
        const proj = await res.json();
        activeProjectId = proj.id;

        // Update local projects list
        const idx = projects.findIndex(s => s.id === id);
        if (idx >= 0) projects[idx] = proj;

        // Show the right phase based on status
        if (proj.status === 'ready') {
            showPhase('chat');
            // Load conversations for this project
            await loadConversations();
        } else if (proj.status === 'processing') {
            showPhase('processing');
            startIngestPolling();
            conversations = [];
            activeConversationId = null;
        } else {
            showPhase('upload');
            conversations = [];
            activeConversationId = null;
        }

        renderSidebar();

        // Load uploaded files and render indexed files panel
        await loadUploadedFiles();
        renderIndexedFiles();
        loadStats();

        // Close mobile sidebar
        document.getElementById('sidebar').classList.remove('open');
    } catch (e) {
        console.error('Failed to activate project', e);
    }
}

async function deleteProject(id) {
    if (!confirm('Delete this project and all its documents?')) return;

    try {
        const res = await fetch(`${API_BASE}/api/chats/delete`, {
            method: 'DELETE',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ chat_id: id })
        });

        if (!res.ok) {
            const err = await res.json().catch(() => ({ error: 'Delete failed' }));
            alert('Failed to delete project: ' + (err.error || 'Unknown error'));
            return;
        }

        projects = projects.filter(s => s.id !== id);
        renderSidebar();

        if (activeProjectId === id) {
            activeProjectId = null;
            activeConversationId = null;
            conversations = [];
            uploadedFiles = [];
            if (projects.length > 0) {
                await activateProject(projects[projects.length - 1].id);
            } else {
                showEmptyState();
            }
        }
    } catch (e) {
        console.error('Failed to delete project', e);
        alert('Failed to delete project: ' + e.message);
    }
}

// ===== Conversation Management =====

async function loadConversations() {
    try {
        const res = await fetch(`${API_BASE}/api/conversations`);
        const data = await res.json();
        conversations = data.conversations || [];
        const activeConvId = data.active_conversation || '';

        if (conversations.length === 0) {
            // Auto-create first conversation
            await createNewConversation();
        } else if (activeConvId) {
            await selectConversation(activeConvId);
        } else {
            await selectConversation(conversations[conversations.length - 1].id);
        }
    } catch (e) {
        conversations = [];
    }
    renderSidebar();
}

async function createNewConversation() {
    if (!activeProjectId) return;

    try {
        const res = await fetch(`${API_BASE}/api/conversations`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: '' })
        });
        const conv = await res.json();
        conversations.push(conv);
        activeConversationId = conv.id;
        convHasBeenNamed = false;
        document.getElementById('conversationThread').innerHTML = '';
        renderSidebar();
    } catch (e) {
        console.error('Failed to create conversation', e);
    }
}

async function selectConversation(convId) {
    activeConversationId = convId;
    convHasBeenNamed = false;
    const thread = document.getElementById('conversationThread');
    thread.innerHTML = '';

    // Check if conversation has a non-default name
    const conv = conversations.find(c => c.id === convId);
    if (conv && conv.name && !conv.name.startsWith('Chat ')) {
        convHasBeenNamed = true;
    }

    // Load messages from backend
    try {
        const res = await fetch(`${API_BASE}/api/conversations/messages`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ conversation_id: convId })
        });
        const msgs = await res.json();

        // Render each message
        if (Array.isArray(msgs)) {
            for (const msg of msgs) {
                if (msg.role === 'user') {
                    const qDiv = document.createElement('div');
                    qDiv.className = 'msg-question';
                    qDiv.textContent = msg.content;
                    thread.appendChild(qDiv);
                } else if (msg.role === 'assistant') {
                    // Rebuild answer object from metadata
                    const meta = msg.metadata || {};
                    appendAnswer({
                        answer: {
                            answer: msg.content,
                            documents: meta.documents || [],
                            pages: meta.pages || [],
                            footnotes: meta.footnotes || [],
                            confidence: meta.confidence || 0,
                            confidence_reason: meta.confidence_reason || ''
                        },
                        time_seconds: meta.time_seconds || 0
                    });
                }
            }
        }
    } catch (e) {
        console.error('Failed to load messages', e);
    }

    renderSidebar();
    scrollThread();
}

async function deleteConversation(convId) {
    try {
        await fetch(`${API_BASE}/api/conversations/delete`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ conversation_id: convId })
        });
        conversations = conversations.filter(c => c.id !== convId);

        if (activeConversationId === convId) {
            if (conversations.length > 0) {
                await selectConversation(conversations[conversations.length - 1].id);
            } else {
                await createNewConversation();
            }
        }
        renderSidebar();
    } catch (e) {
        console.error('Failed to delete conversation', e);
    }
}

// ===== Sidebar Rendering =====

function renderSidebar() {
    const list = document.getElementById('chatList');
    if (projects.length === 0) {
        list.innerHTML = `<div class="sidebar-empty">
            <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.4">
                <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path>
            </svg>
            No projects yet
        </div>`;
        return;
    }

    list.innerHTML = projects.map(proj => {
        const isActive = proj.id === activeProjectId;
        const date = new Date(proj.created_at);
        const dateStr = date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
        const meta = proj.status === 'ready' ? `${proj.file_count} files \u2022 ${proj.chunk_count} chunks` :
            proj.status === 'processing' ? 'Processing...' :
                `${proj.file_count || 0} files uploaded`;

        // Conversation sub-list (only for active project)
        let convsHtml = '';
        if (isActive && proj.status === 'ready' && conversations.length > 0) {
            convsHtml = `<div class="conv-list">
                ${conversations.map(c => {
                const isActiveConv = c.id === activeConversationId;
                return `<div class="conv-item ${isActiveConv ? 'active' : ''}" onclick="event.stopPropagation(); selectConversation('${c.id}')">
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"></path>
                        </svg>
                        <span class="conv-name">${escapeHtml(c.name)}</span>
                        ${conversations.length > 1 ? `<button class="conv-delete" onclick="event.stopPropagation(); deleteConversation('${c.id}')" title="Delete">\u00d7</button>` : ''}
                    </div>`;
            }).join('')}
                <div class="conv-item conv-new" onclick="event.stopPropagation(); createNewConversation()">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <line x1="12" y1="5" x2="12" y2="19"></line>
                        <line x1="5" y1="12" x2="19" y2="12"></line>
                    </svg>
                    <span>New Chat</span>
                </div>
            </div>`;
        }

        return `<div class="project-item ${isActive ? 'active' : ''}">
            <div class="project-header" onclick="activateProject('${proj.id}')">
                <div class="project-info">
                    <div class="project-name" id="project-name-${proj.id}">
                        <span class="chat-item-status ${proj.status}"></span>
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="opacity:0.6; flex-shrink:0">
                            <path d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z"></path>
                        </svg>
                        ${escapeHtml(proj.name)}
                    </div>
                    <div class="chat-item-meta">${dateStr} \u2022 ${meta}</div>
                </div>
                <div class="project-actions">
                    <button class="chat-item-rename" onclick="event.stopPropagation(); startRenameProject('${proj.id}')" title="Rename">
                        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path d="M17 3a2.828 2.828 0 114 4L7.5 20.5 2 22l1.5-5.5L17 3z"></path>
                        </svg>
                    </button>
                    <button class="chat-item-delete" onclick="event.stopPropagation(); deleteProject('${proj.id}')" title="Delete">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <polyline points="3 6 5 6 21 6"></polyline>
                            <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"></path>
                        </svg>
                    </button>
                </div>
            </div>
            ${convsHtml}
        </div>`;
    }).join('');
}

// ===== Project Rename =====

function startRenameProject(projId) {
    const nameEl = document.getElementById(`project-name-${projId}`);
    if (!nameEl) return;

    const proj = projects.find(s => s.id === projId);
    if (!proj) return;

    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'chat-item-rename-input';
    input.value = proj.name;
    input.onclick = (e) => e.stopPropagation();

    const finishRename = async () => {
        const newName = input.value.trim();
        if (newName && newName !== proj.name) {
            await renameProject(projId, newName);
        } else {
            renderSidebar();
        }
    };

    input.addEventListener('blur', finishRename);
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
        if (e.key === 'Escape') { renderSidebar(); }
    });

    nameEl.innerHTML = '';
    nameEl.appendChild(input);
    input.focus();
    input.select();
}

async function renameProject(projId, newName) {
    try {
        await fetch(`${API_BASE}/api/chats/rename`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ chat_id: projId, name: newName })
        });
        const idx = projects.findIndex(s => s.id === projId);
        if (idx >= 0) projects[idx].name = newName;
        renderSidebar();
    } catch (e) {
        console.error('Rename failed', e);
        renderSidebar();
    }
}

// Auto-name the conversation from the first question
async function autoNameConversation(question) {
    if (convHasBeenNamed || !activeConversationId) return;
    convHasBeenNamed = true;

    let title = question.replace(/\n/g, ' ').trim();
    if (title.length > 40) {
        title = title.substring(0, 40).replace(/\s+\S*$/, '') + '\u2026';
    }
    if (title.length < 3) return;

    try {
        await fetch(`${API_BASE}/api/conversations/rename`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ conversation_id: activeConversationId, name: title })
        });
        const idx = conversations.findIndex(c => c.id === activeConversationId);
        if (idx >= 0) conversations[idx].name = title;
        renderSidebar();
    } catch (e) {
        console.error('Auto-name failed', e);
    }
}

// ===== Indexed Files Panel =====

function renderIndexedFiles() {
    const countEl = document.getElementById('indexedFilesCount');
    const listEl = document.getElementById('indexedFilesList');

    if (uploadedFiles.length === 0) {
        countEl.textContent = 'No Documents Indexed';
        listEl.innerHTML = '';
        return;
    }

    countEl.textContent = `${uploadedFiles.length} Document${uploadedFiles.length === 1 ? '' : 's'} Indexed`;

    listEl.innerHTML = uploadedFiles.map(f => {
        const ext = f.name.toLowerCase().split('.').pop();
        return `<span class="indexed-file-tag">
            <span class="file-ext ${ext}">${ext}</span>
            ${escapeHtml(f.name)}
        </span>`;
    }).join('');
}

// ===== Phase Management =====

function showPhase(phase) {
    currentPhase = phase;
    hideEmptyState();
    document.getElementById('uploadPhase').classList.toggle('hidden', phase !== 'upload');
    document.getElementById('processingPhase').classList.toggle('hidden', phase !== 'processing');
    document.getElementById('chatPhase').classList.toggle('hidden', phase !== 'chat');

    if (phase === 'chat') {
        document.getElementById('batchResults').classList.add('hidden');
        renderIndexedFiles();
    }
}

// ===== File Upload =====

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
        projects = await chatsRes.json(); // Changed chatSessions to projects
        renderSidebar(); // Changed renderChatList to renderSidebar
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
    try {
        const res = await fetch(`${API_BASE}/api/files`);
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
            body: JSON.stringify({ name: name })
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

// ===== Ingestion =====

async function startIngestion() {
    if (!activeProjectId) return;

    try {
        const res = await fetch(`${API_BASE}/api/ingest`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' }
        });

        if (!res.ok) {
            const err = await res.json();
            alert('Failed to start processing: ' + (err.error || 'Unknown error'));
            return;
        }

        showPhase('processing');
        startIngestPolling();
    } catch (e) {
        alert('Error: ' + e.message);
    }
}

function startIngestPolling() {
    if (ingestPollInterval) clearInterval(ingestPollInterval);

    updateIngestUI({ phase: 'extracting', files_done: 0, files_total: 0, chunks_done: 0, chunks_total: 0 });

    ingestPollInterval = setInterval(async () => {
        try {
            const res = await fetch(`${API_BASE}/api/ingest/status`);
            const status = await res.json();
            updateIngestUI(status);

            if (status.phase === 'done') {
                clearInterval(ingestPollInterval);
                ingestPollInterval = null;

                // Refresh projects
                const projRes = await fetch(`${API_BASE}/api/chats`);
                projects = await projRes.json();
                renderSidebar();
                loadStats();

                // Load conversations (auto-creates first one if needed)
                setTimeout(async () => {
                    showPhase('chat');
                    await loadConversations();
                }, 800);
            } else if (status.phase === 'error') {
                clearInterval(ingestPollInterval);
                ingestPollInterval = null;
                alert('Processing failed: ' + (status.error || 'Unknown error'));
                showPhase('upload');
            } else if (status.phase === 'cancelled') {
                clearInterval(ingestPollInterval);
                ingestPollInterval = null;
                showPhase('upload');
            } else if (status.phase === 'idle') {
                // Backend goroutine exited (e.g. crash/error) but project still says "processing"
                clearInterval(ingestPollInterval);
                ingestPollInterval = null;
                alert('Processing stopped unexpectedly. Please try again.');
                showPhase('upload');
            }
        } catch (e) {
            console.error('Poll error:', e);
        }
    }, 1500);
}

function updateIngestUI(status) {
    const phaseLabel = document.getElementById('processingPhaseLabel');
    const progressFill = document.getElementById('progressFill');
    const progressFiles = document.getElementById('progressFiles');
    const progressChunks = document.getElementById('progressChunks');
    const title = document.getElementById('processingTitle');

    switch (status.phase) {
        case 'extracting':
            phaseLabel.textContent = 'Extracting text from documents...';
            title.textContent = 'Processing Documents...';
            break;
        case 'embedding':
            phaseLabel.textContent = 'Generating embeddings (this may take a while)...';
            title.textContent = 'Building Knowledge Index...';
            break;
        case 'done':
            phaseLabel.textContent = 'Processing complete!';
            title.textContent = 'Ready to Chat!';
            break;
        default:
            phaseLabel.textContent = status.phase;
    }

    progressFiles.textContent = `${status.files_done} / ${status.files_total} files`;
    progressChunks.textContent = `${status.chunks_done} chunks`;

    // Calculate progress
    let pct = 0;
    if (status.phase === 'extracting' && status.files_total > 0) {
        pct = (status.files_done / status.files_total) * 50; // extraction is 0-50%
    } else if (status.phase === 'embedding') {
        pct = 50 + (status.chunks_total > 0 ? (status.chunks_done / status.chunks_total) * 50 : 0);
    } else if (status.phase === 'done') {
        pct = 100;
    }
    progressFill.style.width = `${pct}%`;
}

async function cancelIngestion() {
    const btn = document.getElementById('cancelIngestBtn');
    btn.disabled = true;
    btn.textContent = 'Cancelling...';

    try {
        const res = await fetch(`${API_BASE}/api/ingest/cancel`, { method: 'POST' });
        if (!res.ok) {
            // No ingestion running — force return to upload anyway
            if (ingestPollInterval) {
                clearInterval(ingestPollInterval);
                ingestPollInterval = null;
            }
            showPhase('upload');
        }
    } catch (e) {
        console.error('Cancel error:', e);
        // Force return to upload on network error too
        if (ingestPollInterval) {
            clearInterval(ingestPollInterval);
            ingestPollInterval = null;
        }
        showPhase('upload');
    }

    btn.disabled = false;
    btn.innerHTML = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg> Cancel Processing`;
}

// ===== Settings =====

async function loadSettings() {
    try {
        const res = await fetch(`${API_BASE}/api/settings`);
        const s = await res.json();
        document.getElementById('settingsLLM').value = s.default_llm || 'openai';
        document.getElementById('settingsEmbed').value = s.embed_provider || 'openai';
        document.getElementById('settingsOpenAIKey').value = s.openai_key || '';
        document.getElementById('settingsOpenAIKey').placeholder = s.openai_key ? s.openai_key : 'sk-...';
        document.getElementById('settingsAnthropicKey').value = s.anthropic_key || '';
        document.getElementById('settingsAnthropicKey').placeholder = s.anthropic_key ? s.anthropic_key : 'sk-ant-...';
        document.getElementById('settingsHFKey').value = s.huggingface_key || '';
        document.getElementById('settingsHFKey').placeholder = s.huggingface_key ? s.huggingface_key : 'hf_...';
        // Clear the actual value fields — only show masked placeholder
        document.getElementById('settingsOpenAIKey').value = '';
        document.getElementById('settingsAnthropicKey').value = '';
        document.getElementById('settingsHFKey').value = '';
        // OCR settings
        document.getElementById('settingsOCR').value = s.ocr_provider || '';
        document.getElementById('sarvamKeyGroup').style.display = s.ocr_provider === 'sarvam' ? '' : 'none';
        document.getElementById('settingsSarvamKey').value = '';
        document.getElementById('settingsSarvamKey').placeholder = s.sarvam_key ? s.sarvam_key : 'sarvam_...';
        // Show tesseract availability
        const tsStatus = document.getElementById('tesseractStatus');
        if (s.tesseract_available) {
            tsStatus.textContent = '(✓ installed)';
            tsStatus.style.color = '#10b981';
        } else {
            tsStatus.textContent = '(not found)';
            tsStatus.style.color = '#f59e0b';
        }
        document.getElementById('settingsHint').textContent = '';
    } catch (e) {
        console.error('Failed to load settings', e);
    }
}

async function saveSettings() {
    const btn = document.getElementById('settingsSaveBtn');
    const hint = document.getElementById('settingsHint');
    btn.disabled = true;

    const body = {
        default_llm: document.getElementById('settingsLLM').value,
        embed_provider: document.getElementById('settingsEmbed').value,
        ocr_provider: document.getElementById('settingsOCR').value,
    };

    // Only send keys if user typed a new one (not empty)
    const oaKey = document.getElementById('settingsOpenAIKey').value.trim();
    const antKey = document.getElementById('settingsAnthropicKey').value.trim();
    const hfKey = document.getElementById('settingsHFKey').value.trim();
    if (oaKey) body.openai_key = oaKey;
    if (antKey) body.anthropic_key = antKey;
    if (hfKey) body.huggingface_key = hfKey;
    const sarvamKey = document.getElementById('settingsSarvamKey').value.trim();
    if (sarvamKey) body.sarvam_key = sarvamKey;

    try {
        const res = await fetch(`${API_BASE}/api/settings`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
        if (res.ok) {
            hint.textContent = '✓ Settings saved! Changes take effect immediately.';
            hint.style.color = '#10b981';
            // Refresh providers
            loadProviders();
        } else {
            hint.textContent = '✗ Failed to save settings.';
            hint.style.color = '#ef4444';
        }
    } catch (e) {
        hint.textContent = '✗ Error: ' + e.message;
        hint.style.color = '#ef4444';
    }

    btn.disabled = false;
    setTimeout(() => { hint.textContent = ''; }, 4000);
}

// ===== Reset =====

async function resetChat() {
    if (!activeProjectId) return;
    if (!confirm('This will delete all uploaded files and indexes for this chat. Continue?')) return;

    try {
        await fetch(`${API_BASE}/api/files`, { method: 'DELETE' });
        uploadedFiles = [];
        renderFileList();
        showPhase('upload');
        loadStats();

        // Refresh projects
        const projRes = await fetch(`${API_BASE}/api/chats`);
        projects = await projRes.json();
        renderSidebar();
    } catch (e) {
        alert('Reset error: ' + e.message);
    }
}

// ===== Provider / Model =====

function setProvider(provider) {
    currentProvider = provider;
    document.querySelectorAll('.provider-btn').forEach(b => b.classList.remove('active'));
    document.querySelector(`[data-provider="${provider}"]`).classList.add('active');
    updateModelDropdown();
}

function updateModelDropdown() {
    const select = document.getElementById('modelSelect');
    select.innerHTML = '';

    const models = providerModels[currentProvider] || [];
    if (models.length === 0) {
        const opt = document.createElement('option');
        opt.value = '';
        opt.textContent = 'Default';
        select.appendChild(opt);
    } else {
        models.forEach((m, i) => {
            const opt = document.createElement('option');
            opt.value = m.id;
            opt.textContent = m.name;
            if (i === 0) opt.selected = true;
            select.appendChild(opt);
        });
    }
    currentModel = select.value;
}

// ===== Mode switching =====

function switchMode(mode) {
    currentMode = mode;
    document.querySelectorAll('.mode-btn').forEach(b => b.classList.remove('active'));
    document.querySelector(`[data-mode="${mode}"]`).classList.add('active');
    document.getElementById('singlePanel').classList.toggle('hidden', mode !== 'single');
    document.getElementById('batchPanel').classList.toggle('hidden', mode !== 'batch');
    document.getElementById('singleResult').classList.add('hidden');
    document.getElementById('batchResults').classList.add('hidden');
}

// ===== Stats =====

async function loadStats() {
    try {
        const res = await fetch(`${API_BASE}/api/stats`);
        const data = await res.json();
        document.getElementById('statDocs').textContent = data.documents;
        document.getElementById('statChunks').textContent = data.chunks.toLocaleString();
        document.getElementById('statStatus').innerHTML = data.index_ready
            ? '<span class="pulse"></span> Ready'
            : '<span class="pulse" style="background:var(--warning)"></span> No Index';

        availableProviders = data.providers || [];
        if (data.default_llm) currentProvider = data.default_llm;

        document.querySelectorAll('.provider-btn').forEach(btn => {
            const prov = btn.dataset.provider;
            if (!availableProviders.includes(prov)) {
                btn.classList.add('unavailable');
                btn.title = 'No API key configured';
            } else {
                btn.classList.remove('unavailable');
                btn.title = '';
            }
            btn.classList.toggle('active', prov === currentProvider);
        });
    } catch (e) {
        document.getElementById('statStatus').innerHTML = '<span class="pulse" style="background:var(--danger)"></span> Offline';
    }
}

async function loadProviders() {
    try {
        const res = await fetch(`${API_BASE}/api/providers`);
        providerModels = await res.json();
        updateModelDropdown();
    } catch (e) {
        console.error('Failed to load providers', e);
    }
}

// ===== Single Query =====

async function submitQuery() {
    const input = document.getElementById('queryInput');
    const question = input.value.trim();
    if (!question) return;

    const thread = document.getElementById('conversationThread');
    const loading = document.getElementById('loadingIndicator');

    // Append user question bubble to thread
    const qDiv = document.createElement('div');
    qDiv.className = 'msg-question';
    qDiv.textContent = question;
    thread.appendChild(qDiv);

    // Clear input for next question
    input.value = '';

    // Show loading inside thread
    loading.classList.remove('hidden');
    scrollThread();

    try {
        const res = await fetch(`${API_BASE}/api/query`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ question, provider: currentProvider, model: currentModel })
        });

        if (!res.ok) {
            const errData = await res.json().catch(() => ({ error: 'Unknown error' }));
            throw new Error(errData.error || 'Request failed');
        }

        const data = await res.json();
        loading.classList.add('hidden');
        appendAnswer(data);

        // Auto-name the conversation from the first question
        autoNameConversation(question);
    } catch (e) {
        loading.classList.add('hidden');
        // Show error in thread as a message
        const errDiv = document.createElement('div');
        errDiv.className = 'msg-answer';
        errDiv.innerHTML = `<div class="msg-answer-header"><span class="msg-answer-label" style="color:var(--danger)">Error</span></div><div class="msg-answer-text" style="color:var(--danger)">${escapeHtml(e.message)}</div>`;
        thread.appendChild(errDiv);
        scrollThread();
    }
}

function appendAnswer(data) {
    const thread = document.getElementById('conversationThread');
    const answer = data.answer;
    const modelLabel = currentModel || 'default';
    const timeSec = data.time_seconds ? data.time_seconds.toFixed(2) + 's' : '';

    // Build the answer text with footnote markers
    let answerHtml = escapeHtml(answer.answer || '');

    // Replace [N] markers with styled footnote refs
    answerHtml = answerHtml.replace(/\[(\d+)\]/g, (match, num) => {
        return `<span class="footnote-ref">${num}</span>`;
    });

    // Build footnotes section
    let footnotesHtml = '';
    let footnotes = answer.footnotes || [];

    // If the LLM didn't return a footnotes array, build one from legacy documents/pages
    if (footnotes.length === 0 && answer.documents && answer.documents.length > 0) {
        footnotes = answer.documents.map((doc, i) => ({
            id: i + 1,
            document: doc,
            page: answer.pages && answer.pages[i] ? answer.pages[i] : 0
        }));
    }

    if (footnotes.length > 0) {
        footnotesHtml = `
            <div class="msg-footnotes">
                <div class="msg-footnotes-title">Sources</div>
                ${footnotes.map(fn =>
            `<div class="footnote-item">
                        <span class="footnote-num">${fn.id}</span>
                        <span class="footnote-doc">${escapeHtml(fn.document)}</span>
                        ${fn.page ? `<span class="footnote-page">p.${fn.page}</span>` : ''}
                    </div>`
        ).join('')}
            </div>`;
    }

    // Build confidence bar + tooltip
    const conf = answer.confidence || 0;
    const confColor = conf > 0.8 ? 'var(--success)' : conf > 0.5 ? 'var(--warning)' : 'var(--danger)';
    const confReason = answer.confidence_reason || getDefaultConfidenceReason(conf);

    const answerDiv = document.createElement('div');
    answerDiv.className = 'msg-answer';
    answerDiv.innerHTML = `
        <div class="msg-answer-header">
            <span class="msg-answer-label">Answer</span>
            <span class="msg-answer-time">${timeSec} \u2022 ${currentProvider} / ${modelLabel}</span>
        </div>
        <div class="msg-answer-text">${answerHtml}</div>
        ${footnotesHtml}
        <div class="msg-confidence">
            <div class="confidence-bar">
                <div class="confidence-fill" style="width:${conf * 100}%; background:${confColor}"></div>
            </div>
            <span class="confidence-text">${(conf * 100).toFixed(0)}%</span>
            <span class="confidence-info">?
                <span class="confidence-tooltip">${escapeHtml(confReason)}</span>
            </span>
        </div>
    `;
    thread.appendChild(answerDiv);
    scrollThread();
}

function getDefaultConfidenceReason(conf) {
    if (conf >= 0.9) return 'Strong match \u2014 answer directly supported by multiple sources.';
    if (conf >= 0.7) return 'Good match \u2014 answer supported by context with minor gaps.';
    if (conf >= 0.5) return 'Moderate \u2014 partial information found, some inference required.';
    if (conf > 0) return 'Low confidence \u2014 limited relevant context found.';
    return 'No relevant information found in the indexed documents.';
}

function scrollThread() {
    const thread = document.getElementById('conversationThread');
    requestAnimationFrame(() => {
        thread.scrollTop = thread.scrollHeight;
    });
}

// Keep legacy renderSingleResult as alias for batch mode
function renderSingleResult(data) {
    appendAnswer(data);
}

// ===== Batch Mode =====

async function runBatch() {
    const btn = document.getElementById('runBatchBtn');
    btn.disabled = true;

    const loading = document.getElementById('loadingIndicator');
    const batchResults = document.getElementById('batchResults');
    batchResults.classList.add('hidden');
    batchResults.innerHTML = '';
    loading.classList.remove('hidden');

    const startTime = performance.now();
    timerInterval = setInterval(() => {
        const elapsed = (performance.now() - startTime) / 1000;
        document.getElementById('timerValue').textContent = `${elapsed.toFixed(2)}s`;
        const pct = Math.min((elapsed / 30) * 100, 100);
        document.getElementById('timerFill').style.width = `${pct}%`;
        if (elapsed > 30) {
            document.getElementById('timerFill').style.background = 'var(--danger)';
        }
    }, 50);

    try {
        const res = await fetch(`${API_BASE}/api/batch`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ questions: TEST_QUESTIONS, provider: currentProvider, model: currentModel })
        });

        if (!res.ok) {
            const errData = await res.json().catch(() => ({ error: 'Unknown error' }));
            throw new Error(errData.error || 'Request failed');
        }

        const data = await res.json();
        clearInterval(timerInterval);
        const elapsed = data.total_time_seconds;
        document.getElementById('timerValue').textContent = `${elapsed.toFixed(2)}s`;
        document.getElementById('timerFill').style.width = `${Math.min((elapsed / 30) * 100, 100)}%`;

        loading.classList.add('hidden');
        renderBatchResults(data);
    } catch (e) {
        clearInterval(timerInterval);
        loading.classList.add('hidden');
        alert('Error: ' + e.message);
    }

    btn.disabled = false;
}

function renderBatchResults(data) {
    const container = document.getElementById('batchResults');
    container.classList.remove('hidden');
    container.innerHTML = '';

    const answered = data.answers.filter(a => a && a.confidence > 0.3).length;
    const avgConf = data.answers.reduce((s, a) => s + (a ? a.confidence : 0), 0) / data.answers.length;

    const summary = document.createElement('div');
    summary.className = 'batch-summary';
    summary.innerHTML = `
        <div class="summary-item">
            <span class="summary-value">${data.total_time_seconds.toFixed(2)}s</span>
            <span class="summary-label">Total Time</span>
        </div>
        <div class="summary-item">
            <span class="summary-value">${answered}/${data.answers.length}</span>
            <span class="summary-label">Answered</span>
        </div>
        <div class="summary-item">
            <span class="summary-value">${(avgConf * 100).toFixed(0)}%</span>
            <span class="summary-label">Avg Confidence</span>
        </div>
        <div class="summary-item">
            <span class="summary-value" style="${data.total_time_seconds < 30 ? 'color:var(--success);-webkit-text-fill-color:var(--success)' : 'color:var(--danger);-webkit-text-fill-color:var(--danger)'}">${data.total_time_seconds < 30 ? 'PASS' : 'FAIL'}</span>
            <span class="summary-label">&lt; 30s Target</span>
        </div>
    `;
    container.appendChild(summary);

    data.answers.forEach((answer, i) => {
        const card = document.createElement('div');
        card.className = 'batch-card';

        if (!answer) {
            card.innerHTML = `
                <div class="batch-question"><span class="batch-q-num">${i + 1}</span>${escapeHtml(TEST_QUESTIONS[i])}</div>
                <div class="batch-answer" style="color:var(--danger)">Error \u2014 no answer returned</div>
            `;
        } else {
            const conf = answer.confidence || 0;
            const confColor = conf > 0.8 ? 'var(--success)' : conf > 0.5 ? 'var(--warning)' : 'var(--danger)';

            let sourcesHtml = '';
            if (answer.documents && answer.documents.length > 0) {
                sourcesHtml = '<div class="result-sources" style="margin-top:0.5rem">';
                answer.documents.forEach((doc, j) => {
                    const page = answer.pages && answer.pages[j] ? answer.pages[j] : '?';
                    sourcesHtml += `<span class="source-tag">${escapeHtml(doc)} <span class="source-page">p.${page}</span></span>`;
                });
                sourcesHtml += '</div>';
            }

            card.innerHTML = `
                <div class="batch-question"><span class="batch-q-num">${i + 1}</span>${escapeHtml(TEST_QUESTIONS[i])}</div>
                <div class="batch-answer">${escapeHtml(answer.answer)}</div>
                ${sourcesHtml}
                <div class="result-confidence" style="margin-top:0.5rem">
                    <div class="confidence-bar">
                        <div class="confidence-fill" style="width:${conf * 100}%;background:${confColor}"></div>
                    </div>
                    <span class="confidence-text">${(conf * 100).toFixed(0)}%</span>
                </div>
            `;
        }
        container.appendChild(card);
    });
}

// ===== Utilities =====

function formatFileSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

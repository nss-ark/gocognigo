// === GoCognigo — Project Management ===

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
        const data = await res.json();
        const proj = data.project;
        const convs = data.conversations || [];
        activeProjectId = proj.id;

        // Update local projects list
        const idx = projects.findIndex(s => s.id === id);
        if (idx >= 0) projects[idx] = proj;

        // Show the right phase based on status
        if (proj.status === 'ready') {
            showPhase('chat');
            // Use conversations from activate response
            conversations = convs;
            if (conversations.length === 0) {
                await createNewConversation();
            } else {
                await selectConversation(conversations[conversations.length - 1].id);
            }
            // Poll index-status until the index is ready
            waitForIndex();
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

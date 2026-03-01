// === GoCognigo — UI Helpers ===

function showEmptyState() {
    document.getElementById('emptyState').classList.remove('hidden');
    document.getElementById('uploadPhase').classList.add('hidden');
    document.getElementById('processingPhase').classList.add('hidden');
    document.getElementById('chatPhase').classList.add('hidden');
}

function hideEmptyState() {
    document.getElementById('emptyState').classList.add('hidden');
}

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

function scrollThread() {
    const thread = document.getElementById('conversationThread');
    if (!thread) return;
    requestAnimationFrame(() => {
        thread.scrollTop = thread.scrollHeight;
    });
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

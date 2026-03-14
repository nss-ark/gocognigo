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
    if (!Array.isArray(projects)) projects = [];
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
        const publishedBadge = proj.published ? ' <span class="published-badge">Published</span>' : '';
        const meta = proj.status === 'ready' ? `${proj.file_count} files \u2022 ${proj.chunk_count} chunks${publishedBadge}` :
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
                        <div class="conv-actions">
                            <button class="conv-export" onclick="event.stopPropagation(); exportConversation('${c.id}')" title="Export as Markdown">
                                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                                    <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"></path>
                                    <polyline points="7 10 12 15 17 10"></polyline>
                                    <line x1="12" y1="15" x2="12" y2="3"></line>
                                </svg>
                            </button>
                            ${conversations.length > 1 ? `<button class="conv-delete" onclick="event.stopPropagation(); deleteConversation('${c.id}')" title="Delete">\u00d7</button>` : ''}
                        </div>
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
                    <button class="chat-item-settings" onclick="event.stopPropagation(); openProjectSettings('${proj.id}')" title="Project Settings">
                        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                            <circle cx="12" cy="12" r="3"></circle>
                            <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"></path>
                        </svg>
                    </button>
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
        const safeName = escapeHtml(f.name).replace(/'/g, "\\'");
        const isPdf = ext === 'pdf';
        return `<span class="indexed-file-tag${isPdf ? ' viewable' : ''}" ${isPdf ? `onclick="openDocViewer('${escapeHtml(activeProjectId)}', '${safeName}', 1)" title="Click to view document"` : ''}>
            <span class="file-ext ${ext}">${ext}</span>
            ${escapeHtml(f.name)}
            ${isPdf ? '<span class="indexed-file-view" title="View PDF">&#128065;</span>' : ''}
            <button class="indexed-file-delete" onclick="event.stopPropagation(); removeFile('${safeName}')" title="Remove document from index">&times;</button>
        </span>`;
    }).join('');
}

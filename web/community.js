// === GoCognigo — Community & Project Settings ===

let communitySearchTimer = null;
let activeCommunityTag = '';

// ===== Project Settings Modal =====

function openProjectSettings(projectId) {
    const proj = projects.find(p => p.id === projectId);
    if (!proj) return;

    document.getElementById('projSettingsName').value = proj.name || '';
    document.getElementById('projSettingsDesc').value = proj.description || '';
    document.getElementById('projSettingsTags').value = (proj.tags || []).join(', ');
    document.getElementById('projSettingsAuthor').value = proj.author || '';
    document.getElementById('projSettingsSystemPrompt').value = proj.system_prompt || '';

    // Update publish button state
    updatePublishUI(proj);

    // Store which project we're editing
    document.getElementById('projectSettingsModal').dataset.projectId = projectId;
    document.getElementById('projectSettingsModal').classList.remove('hidden');
}

function closeProjectSettings() {
    document.getElementById('projectSettingsModal').classList.add('hidden');
}

function updatePublishUI(proj) {
    const statusEl = document.getElementById('publishStatus');
    const btn = document.getElementById('publishBtn');

    if (proj.published) {
        statusEl.innerHTML = `<span class="publish-active">Published ${proj.published_at ? new Date(proj.published_at).toLocaleDateString() : ''}</span>`;
        btn.textContent = 'Unpublish';
        btn.classList.add('unpublish');
    } else {
        statusEl.innerHTML = '';
        btn.textContent = 'Publish to Community';
        btn.classList.remove('unpublish');
    }

    // Disable publish if not ready
    if (proj.status !== 'ready') {
        btn.disabled = true;
        btn.title = 'Process documents first';
    } else {
        btn.disabled = false;
        btn.title = '';
    }
}

async function saveProjectSettings() {
    const modal = document.getElementById('projectSettingsModal');
    const projectId = modal.dataset.projectId;
    if (!projectId) return;

    const name = document.getElementById('projSettingsName').value.trim();
    const description = document.getElementById('projSettingsDesc').value.trim();
    const tagsStr = document.getElementById('projSettingsTags').value.trim();
    const author = document.getElementById('projSettingsAuthor').value.trim();
    const systemPrompt = document.getElementById('projSettingsSystemPrompt').value.trim();

    const tags = tagsStr ? tagsStr.split(',').map(t => t.trim()).filter(t => t) : [];

    try {
        // Update name if changed
        const proj = projects.find(p => p.id === projectId);
        if (proj && name && name !== proj.name) {
            await fetch(`${API_BASE}/api/chats/rename`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ chat_id: projectId, name: name })
            });
        }

        // Update metadata
        const res = await fetch(`${API_BASE}/api/projects/meta`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                project_id: projectId,
                description: description,
                tags: tags,
                system_prompt: systemPrompt,
                author: author
            })
        });

        if (!res.ok) {
            const err = await res.json().catch(() => ({ error: 'Save failed' }));
            alert('Failed to save: ' + (err.error || 'Unknown error'));
            return;
        }

        const updated = await res.json();

        // Update local state
        const idx = projects.findIndex(p => p.id === projectId);
        if (idx >= 0) {
            projects[idx] = { ...projects[idx], ...updated };
            if (name) projects[idx].name = name;
        }

        renderSidebar();
        closeProjectSettings();
    } catch (e) {
        console.error('Failed to save project settings', e);
        alert('Failed to save settings: ' + e.message);
    }
}

async function togglePublish() {
    const modal = document.getElementById('projectSettingsModal');
    const projectId = modal.dataset.projectId;
    if (!projectId) return;

    const proj = projects.find(p => p.id === projectId);
    if (!proj) return;

    const newState = !proj.published;

    // Save settings first if publishing (to ensure description is set)
    if (newState) {
        const desc = document.getElementById('projSettingsDesc').value.trim();
        if (!desc) {
            alert('Please add a description before publishing.');
            document.getElementById('projSettingsDesc').focus();
            return;
        }
        await saveProjectSettings();
    }

    try {
        const res = await fetch(`${API_BASE}/api/projects/publish`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                project_id: projectId,
                publish: newState
            })
        });

        if (!res.ok) {
            const err = await res.json().catch(() => ({ error: 'Publish failed' }));
            alert('Failed: ' + (err.error || 'Unknown error'));
            return;
        }

        const updated = await res.json();
        const idx = projects.findIndex(p => p.id === projectId);
        if (idx >= 0) {
            projects[idx] = { ...projects[idx], ...updated };
        }

        updatePublishUI(projects[idx] || updated);
        renderSidebar();
    } catch (e) {
        console.error('Publish toggle failed', e);
        alert('Failed: ' + e.message);
    }
}

// ===== Community Hub =====

async function openCommunityHub() {
    document.getElementById('communityHubModal').classList.remove('hidden');
    activeCommunityTag = '';
    document.getElementById('communitySearch').value = '';
    await loadCommunityTags();
    await loadCommunityProjects();
}

function closeCommunityHub() {
    document.getElementById('communityHubModal').classList.add('hidden');
}

async function loadCommunityTags() {
    try {
        const res = await fetch(`${API_BASE}/api/community/tags`);
        const tags = await res.json();
        const container = document.getElementById('communityTags');

        if (!tags || tags.length === 0) {
            container.innerHTML = '';
            return;
        }

        container.innerHTML = `<button class="community-tag ${activeCommunityTag === '' ? 'active' : ''}" onclick="filterCommunityByTag('')">All</button>` +
            tags.map(t =>
                `<button class="community-tag ${activeCommunityTag === t.tag ? 'active' : ''}" onclick="filterCommunityByTag('${escapeHtml(t.tag)}')">${escapeHtml(t.tag)} <span class="tag-count">${t.count}</span></button>`
            ).join('');
    } catch (e) {
        console.error('Failed to load tags', e);
    }
}

async function filterCommunityByTag(tag) {
    activeCommunityTag = tag;
    await loadCommunityTags();
    await loadCommunityProjects();
}

function debounceCommunitySearch() {
    if (communitySearchTimer) clearTimeout(communitySearchTimer);
    communitySearchTimer = setTimeout(() => loadCommunityProjects(), 300);
}

async function loadCommunityProjects() {
    const searchQuery = document.getElementById('communitySearch').value.trim();
    let url = `${API_BASE}/api/community`;
    const params = new URLSearchParams();
    if (searchQuery) params.set('q', searchQuery);
    if (activeCommunityTag) params.set('tag', activeCommunityTag);
    if (params.toString()) url += '?' + params.toString();

    try {
        const res = await fetch(url);
        const projects = await res.json();
        renderCommunityGrid(projects);
    } catch (e) {
        console.error('Failed to load community projects', e);
    }
}

function renderCommunityGrid(communityProjects) {
    const grid = document.getElementById('communityGrid');

    if (!communityProjects || communityProjects.length === 0) {
        grid.innerHTML = `<div class="community-empty">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.4">
                <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4-4v2"></path>
                <circle cx="9" cy="7" r="4"></circle>
                <path d="M23 21v-2a4 4 0 00-3-3.87"></path>
                <path d="M16 3.13a4 4 0 010 7.75"></path>
            </svg>
            <p>No published projects found. Be the first to share!</p>
        </div>`;
        return;
    }

    grid.innerHTML = communityProjects.map(proj => {
        const tags = (proj.tags || []).map(t =>
            `<span class="community-card-tag">${escapeHtml(t)}</span>`
        ).join('');

        const date = proj.published_at ? new Date(proj.published_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : '';

        const hasSystemPrompt = proj.system_prompt ? '<span class="community-card-badge" title="Custom system prompt configured">Custom Prompt</span>' : '';

        return `<div class="community-card">
            <div class="community-card-header">
                <h4 class="community-card-title">${escapeHtml(proj.name)}</h4>
                ${hasSystemPrompt}
            </div>
            <p class="community-card-desc">${escapeHtml(proj.description || 'No description')}</p>
            <div class="community-card-tags">${tags}</div>
            <div class="community-card-meta">
                <span>${proj.file_count} docs \u2022 ${proj.chunk_count} chunks</span>
                ${proj.author ? `<span>by ${escapeHtml(proj.author)}</span>` : ''}
                ${date ? `<span>${date}</span>` : ''}
            </div>
            <button class="process-btn community-clone-btn" onclick="cloneCommunityProject('${proj.id}', '${escapeHtml(proj.name).replace(/'/g, "\\'")}')">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"></path>
                </svg>
                Use This Project
            </button>
        </div>`;
    }).join('');
}

async function cloneCommunityProject(sourceId, sourceName) {
    const name = prompt('Name for your copy:', sourceName + ' (copy)');
    if (!name) return;

    try {
        const res = await fetch(`${API_BASE}/api/community/clone`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                source_id: sourceId,
                name: name
            })
        });

        if (!res.ok) {
            const err = await res.json().catch(() => ({ error: 'Clone failed' }));
            alert('Failed to clone: ' + (err.error || 'Unknown error'));
            return;
        }

        const newProj = await res.json();
        projects.push(newProj);
        renderSidebar();
        closeCommunityHub();

        // Activate the new project
        await activateProject(newProj.id);
    } catch (e) {
        console.error('Failed to clone project', e);
        alert('Failed to clone: ' + e.message);
    }
}

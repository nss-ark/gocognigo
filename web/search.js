// === GoCognigo — Document Search ===
// Full-text BM25 keyword search across indexed chunks — no LLM needed.

let searchDebounceTimer = null;

async function performSearch() {
    const input = document.getElementById('searchInput');
    const query = input.value.trim();
    const container = document.getElementById('searchResults');
    const summary = document.getElementById('searchSummary');

    if (!query) {
        container.innerHTML = '<div class="search-empty">Type a keyword to search across your documents</div>';
        summary.textContent = '';
        return;
    }

    if (!activeProjectId) {
        container.innerHTML = '<div class="search-empty">No project selected</div>';
        return;
    }

    try {
        const res = await fetch(`${API_BASE}/api/search`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ query, project_id: activeProjectId })
        });
        const data = await res.json();

        if (!res.ok) {
            container.innerHTML = `<div class="search-empty">${escapeHtml(data.error || 'Search failed')}</div>`;
            summary.textContent = '';
            return;
        }

        renderSearchResults(data, query);
    } catch (e) {
        container.innerHTML = `<div class="search-empty">Error: ${escapeHtml(e.message)}</div>`;
        summary.textContent = '';
    }
}

function renderSearchResults(data, query) {
    const container = document.getElementById('searchResults');
    const summary = document.getElementById('searchSummary');

    const results = data.results || [];
    const total = data.total || 0;
    const timeMs = data.time_ms || 0;

    summary.textContent = `${total} result${total !== 1 ? 's' : ''} in ${timeMs}ms`;

    if (results.length === 0) {
        container.innerHTML = '<div class="search-empty">No results found</div>';
        return;
    }

    container.innerHTML = results.map((r, i) => `
        <div class="search-result-card">
            <div class="search-result-header">
                <span class="search-result-doc">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"></path>
                        <polyline points="14 2 14 8 20 8"></polyline>
                    </svg>
                    ${escapeHtml(r.document)}
                </span>
                <span class="search-result-meta">
                    ${r.page ? `<span class="search-result-page">p.${r.page}</span>` : ''}
                    ${r.section ? `<span class="search-result-section">${escapeHtml(r.section)}</span>` : ''}
                    <span class="search-result-score">${r.score.toFixed(2)}</span>
                </span>
            </div>
            <div class="search-result-text">${r.text}</div>
        </div>
    `).join('');
}

function debounceSearch() {
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(performSearch, 300);
}

function clearSearch() {
    const input = document.getElementById('searchInput');
    input.value = '';
    const container = document.getElementById('searchResults');
    container.innerHTML = '<div class="search-empty">Type a keyword to search across your documents</div>';
    document.getElementById('searchSummary').textContent = '';
    input.focus();
}

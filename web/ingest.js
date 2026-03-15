// === GoCognigo — Ingestion Pipeline ===

async function startIngestion() {
    if (!activeProjectId) return;

    try {
        const res = await fetch(`${API_BASE}/api/ingest`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: activeProjectId })
        });

        if (!res.ok) {
            const err = await res.json();
            const msg = err.error || 'Unknown error';
            // Show setup wizard if this is an API key error
            if (msg.includes('API key') || msg.includes('api key')) {
                const banner = document.getElementById('apiKeyBanner');
                if (banner) banner.classList.remove('hidden');
                openSetupWizard();
            } else {
                alert('Failed to start processing: ' + msg);
            }
            return;
        }

        showPhase('processing');
        startIngestPolling();
    } catch (e) {
        alert('Error: ' + e.message);
    }
}

let ingestStatusWs = null;

function startIngestPolling() {
    if (ingestPollInterval) {
        clearInterval(ingestPollInterval);
        ingestPollInterval = null;
    }
    if (ingestStatusWs) {
        ingestStatusWs.close();
        ingestStatusWs = null;
    }

    updateIngestUI({ phase: 'processing', files_done: 0, files_total: 0, chunks_done: 0, chunks_total: 0 });

    let wsUrl;
    if (API_BASE && API_BASE.startsWith('http')) {
        wsUrl = API_BASE.replace(/^http/, 'ws') + '/api/ingest/ws';
    } else {
        wsUrl = (window.location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + window.location.host + '/api/ingest/ws';
    }

    if (authIdToken) {
        wsUrl += '?token=' + encodeURIComponent(authIdToken);
    }
    ingestStatusWs = new WebSocket(wsUrl);

    ingestStatusWs.onmessage = async (e) => {
        try {
            const status = JSON.parse(e.data);
            updateIngestUI(status);

            if (status.phase === 'done') {
                ingestStatusWs.close();
                ingestStatusWs = null;

                // Refresh projects
                await refreshProjects();
                renderSidebar();
                loadStats();

                showIngestResults(status);
            } else if (status.phase === 'error') {
                ingestStatusWs.close();
                ingestStatusWs = null;

                if (status.can_retry) {
                    showRetryUI(status.error || 'Embedding failed');
                } else {
                    alert('Processing failed: ' + (status.error || 'Unknown error'));
                    showPhase('upload');
                }
            } else if (status.phase === 'cancelled') {
                ingestStatusWs.close();
                ingestStatusWs = null;
                showPhase('upload');
            } else if (status.phase === 'idle') {
                ingestStatusWs.close();
                ingestStatusWs = null;
                alert('Processing stopped unexpectedly. Please try again.');
                showPhase('upload');
            }
        } catch (err) {
            console.error('WS message error:', err);
        }
    };

    ingestStatusWs.onerror = (e) => {
        console.error('WebSocket error:', e);
    };

    ingestStatusWs.onclose = () => {
        ingestStatusWs = null;
        // Optionally handle unexpected closure here
    };
}

function updateIngestUI(status) {
    const phaseLabel = document.getElementById('processingPhaseLabel');
    const progressFill = document.getElementById('progressFill');
    const progressFiles = document.getElementById('progressFiles');
    const progressChunks = document.getElementById('progressChunks');
    const title = document.getElementById('processingTitle');

    switch (status.phase) {
        case 'extracting':
        // Legacy/backward compat — treat like processing
        case 'processing':
            // Show combined extraction + embedding progress
            if (status.chunks_total > 0) {
                phaseLabel.textContent = `Extracting & embedding documents...`;
                title.textContent = 'Building Knowledge Index...';
            } else {
                phaseLabel.textContent = 'Extracting text from documents...';
                title.textContent = 'Processing Documents...';
            }
            break;
        case 'embedding':
            // Legacy compat
            phaseLabel.textContent = 'Generating embeddings...';
            title.textContent = 'Building Knowledge Index...';
            break;
        case 'done':
            phaseLabel.textContent = 'Processing complete!';
            title.textContent = 'Ready to Chat!';
            break;
        default:
            phaseLabel.textContent = status.phase;
    }

    progressFiles.textContent = `${status.files_done} / ${status.files_total} files extracted`;
    if (status.chunks_total > 0) {
        progressChunks.textContent = `${status.chunks_done} / ${status.chunks_total} chunks embedded`;
    } else {
        progressChunks.textContent = `${status.chunks_done} chunks`;
    }

    // Calculate progress: extraction is 30%, embedding is 70% (embedding is the bottleneck)
    let pct = 0;
    if (status.phase === 'processing' || status.phase === 'extracting') {
        const extractPct = status.files_total > 0 ? (status.files_done / status.files_total) * 30 : 0;
        const embedPct = status.chunks_total > 0 ? (status.chunks_done / status.chunks_total) * 70 : 0;
        pct = extractPct + embedPct;
    } else if (status.phase === 'embedding') {
        pct = 30 + (status.chunks_total > 0 ? (status.chunks_done / status.chunks_total) * 70 : 0);
    } else if (status.phase === 'done') {
        pct = 100;
    }
    progressFill.style.width = `${pct}%`;
}

async function cancelIngestion() {
    const btn = document.getElementById('cancelIngestBtn');
    btn.disabled = true;
    btn.textContent = 'Cancelling...';

    // Always close WebSocket immediately so we don't receive stale status updates
    if (ingestStatusWs) {
        ingestStatusWs.close();
        ingestStatusWs = null;
    }
    if (ingestPollInterval) {
        clearInterval(ingestPollInterval);
        ingestPollInterval = null;
    }

    try {
        await fetch(`${API_BASE}/api/ingest/cancel`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: activeProjectId })
        });
    } catch (e) {
        console.error('Cancel error:', e);
    }

    // Always navigate back to upload phase regardless of server response
    showPhase('upload');

    btn.disabled = false;
    btn.innerHTML = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg> Cancel Processing`;
}

// Show retry UI when embedding fails but extraction succeeded
function showRetryUI(errorMsg) {
    const title = document.getElementById('processingTitle');
    const phaseLabel = document.getElementById('processingPhaseLabel');
    const progressFill = document.getElementById('progressFill');
    const retryBar = document.getElementById('retryBar');
    const retryError = document.getElementById('retryError');

    title.textContent = 'Embedding Failed';
    phaseLabel.textContent = 'Text extraction succeeded — you can retry embedding after fixing the issue.';
    progressFill.style.width = '30%'; // show extraction completed
    progressFill.style.background = 'var(--warning)';

    // Hide spinner and cancel, show retry bar
    document.querySelector('.processing-spinner').style.display = 'none';
    document.getElementById('cancelIngestBtn').classList.add('hidden');
    retryBar.classList.remove('hidden');
    retryError.textContent = errorMsg;
}

async function retryIngestion() {
    if (!activeProjectId) return;

    const retryBtn = document.getElementById('retryEmbedBtn');
    retryBtn.disabled = true;
    retryBtn.textContent = 'Retrying...';

    // Reset UI
    document.querySelector('.processing-spinner').style.display = '';
    document.getElementById('retryBar').classList.add('hidden');
    document.getElementById('cancelIngestBtn').classList.remove('hidden');
    document.getElementById('progressFill').style.background = '';

    try {
        const res = await fetch(`${API_BASE}/api/ingest/retry`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: activeProjectId })
        });

        if (!res.ok) {
            const err = await res.json();
            alert('Retry failed: ' + (err.error || 'Unknown error'));
            showPhase('upload');
            return;
        }

        // Resume polling
        startIngestPolling();
    } catch (e) {
        alert('Retry error: ' + e.message);
        showPhase('upload');
    } finally {
        retryBtn.disabled = false;
        retryBtn.textContent = 'Retry Embedding';
    }
}

async function backToUploadFromRetry() {
    // Cancel retry state and go back to upload
    try {
        await fetch(`${API_BASE}/api/ingest/cancel`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: activeProjectId })
        });
    } catch (e) {
        // ignore
    }
    document.querySelector('.processing-spinner').style.display = '';
    document.getElementById('retryBar').classList.add('hidden');
    document.getElementById('cancelIngestBtn').classList.remove('hidden');
    document.getElementById('progressFill').style.background = '';
    showPhase('upload');
}

// Show per-file results when ingestion completes
function showIngestResults(status) {
    const results = status.file_results || [];
    const succeeded = results.filter(f => f.status === 'ok');
    const failed = results.filter(f => f.status === 'failed');
    const totalChunks = succeeded.reduce((sum, f) => sum + (f.chunks || 0), 0);

    // Hide spinner & cancel, show results
    document.querySelector('.processing-spinner').style.display = 'none';
    document.getElementById('cancelIngestBtn').classList.add('hidden');
    document.getElementById('ingestResults').classList.remove('hidden');

    // Update title
    const title = document.getElementById('ingestResultsTitle');
    if (failed.length === 0) {
        title.textContent = '✓ All Files Processed Successfully';
        title.style.color = 'var(--success)';
    } else if (succeeded.length === 0) {
        title.textContent = '✗ Processing Failed';
        title.style.color = 'var(--danger)';
    } else {
        title.textContent = `⚠ Processed ${succeeded.length} of ${results.length} Files`;
        title.style.color = 'var(--warning)';
    }

    // Summary line
    document.getElementById('ingestResultsSummary').textContent =
        `${succeeded.length} succeeded · ${failed.length} failed · ${totalChunks.toLocaleString()} chunks extracted`;

    // Build file results list
    const listEl = document.getElementById('ingestResultsList');
    listEl.innerHTML = results.map(f => {
        const isOk = f.status === 'ok';
        const ext = f.name.toLowerCase().split('.').pop();
        const icon = isOk ? '✓' : '✗';
        const statusClass = isOk ? 'result-ok' : 'result-fail';
        const detail = isOk
            ? `${f.chunks} chunk${f.chunks !== 1 ? 's' : ''}`
            : escapeHtml(f.error || 'Unknown error');
        return `<div class="ingest-result-item ${statusClass}">
            <span class="result-icon">${icon}</span>
            <span class="file-ext ${ext}">${ext}</span>
            <span class="result-name">${escapeHtml(f.name)}</span>
            <span class="result-detail">${detail}</span>
        </div>`;
    }).join('');

    // Hide continue button if no files succeeded
    document.getElementById('continueToChatBtn').classList.toggle('hidden', succeeded.length === 0);
}

// Transition from results summary to chat
async function continueToChat() {
    // Reset results UI for next time
    document.querySelector('.processing-spinner').style.display = '';
    document.getElementById('cancelIngestBtn').classList.remove('hidden');
    document.getElementById('ingestResults').classList.add('hidden');

    showPhase('chat');
    await loadConversations();
}

// Poll /api/index-status until the index is loaded, showing a loading banner
function waitForIndex() {
    const thread = document.getElementById('conversationThread');
    const input = document.getElementById('queryInput');
    const submitBtn = document.getElementById('submitBtn');

    // Check right away — might already be cached
    fetch(`${API_BASE}/api/index-status?project_id=${activeProjectId}`).then(r => r.json()).then(data => {
        if (data.ready) return; // Already loaded (cache hit)

        // Show loading banner
        const banner = document.createElement('div');
        banner.id = 'indexLoadingBanner';
        banner.className = 'index-loading-banner';
        banner.innerHTML = `
            <div class="spinner" style="width:16px;height:16px;border-width:2px"></div>
            <span>Loading document index...</span>
        `;
        thread.prepend(banner);
        input.disabled = true;
        submitBtn.disabled = true;

        // Poll every 300ms
        const poll = setInterval(async () => {
            try {
                const res = await fetch(`${API_BASE}/api/index-status?project_id=${activeProjectId}`);
                const status = await res.json();
                if (status.ready) {
                    clearInterval(poll);
                    input.disabled = false;
                    submitBtn.disabled = false;
                    const el = document.getElementById('indexLoadingBanner');
                    if (el) {
                        el.style.opacity = '0';
                        setTimeout(() => el.remove(), 300);
                    }
                    loadStats();
                }
            } catch (e) {
                // ignore poll errors
            }
        }, 300);
    });
}

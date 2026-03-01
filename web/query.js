// === GoCognigo — Query, Batch, Settings, Providers ===

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
            body: JSON.stringify({ question, provider: currentProvider, model: currentModel, project_id: activeProjectId, conversation_id: activeConversationId })
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

    // Build the answer text with markdown rendering
    let answerHtml = renderMarkdown(answer.answer || '');

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

    // Build thinking toggle (collapsed by default)
    let thinkingHtml = '';
    if (answer.thinking) {
        const thinkingText = escapeHtml(answer.thinking).replace(/\n/g, '<br>');
        thinkingHtml = `
            <div class="msg-thinking">
                <button class="msg-thinking-toggle" onclick="this.parentElement.classList.toggle('open')">
                    <span class="thinking-icon">\uD83E\uDDE0</span>
                    <span class="thinking-label">Show reasoning</span>
                    <span class="thinking-chevron">\u25B6</span>
                </button>
                <div class="msg-thinking-content">${thinkingText}</div>
            </div>
        `;
    }

    const answerDiv = document.createElement('div');
    answerDiv.className = 'msg-answer';
    answerDiv.innerHTML = `
        <div class="msg-answer-header">
            <span class="msg-answer-label">Answer</span>
            <span class="msg-answer-time">${timeSec} \u2022 ${currentProvider} / ${modelLabel}</span>
        </div>
        ${thinkingHtml}
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
            body: JSON.stringify({ questions: batchQuestions, provider: currentProvider, model: currentModel, project_id: activeProjectId })
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
                <div class="batch-question"><span class="batch-q-num">${i + 1}</span>${escapeHtml(batchQuestions[i] || 'Question ' + (i + 1))}</div>
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
                <div class="batch-question"><span class="batch-q-num">${i + 1}</span>${escapeHtml(batchQuestions[i] || 'Question ' + (i + 1))}</div>
                <div class="batch-answer">${renderMarkdown(answer.answer)}</div>
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
        await fetch(`${API_BASE}/api/files`, {
            method: 'DELETE',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ project_id: activeProjectId })
        });
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
        const url = activeProjectId ? `${API_BASE}/api/stats?project_id=${activeProjectId}` : `${API_BASE}/api/stats`;
        const res = await fetch(url);
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

// ===== Batch Question Editor =====

function saveBatchQuestions() {
    localStorage.setItem('batchQuestions', JSON.stringify(batchQuestions));
}

function renderBatchQuestions() {
    const container = document.getElementById('batchQuestionsList');
    if (!container) return;

    container.innerHTML = batchQuestions.map((q, i) => `
        <div class="batch-q-item" data-index="${i}">
            <span class="batch-q-num">${i + 1}</span>
            <input type="text" class="batch-q-input" value="${escapeHtml(q)}"
                onchange="updateBatchQuestion(${i}, this.value)"
                onblur="updateBatchQuestion(${i}, this.value)"
                placeholder="Enter question...">
            <button class="batch-q-remove" onclick="removeBatchQuestion(${i})" title="Remove">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"></line>
                    <line x1="6" y1="6" x2="18" y2="18"></line>
                </svg>
            </button>
        </div>
    `).join('');
}

function addBatchQuestion() {
    batchQuestions.push('');
    saveBatchQuestions();
    renderBatchQuestions();
    // Focus the new empty input
    const inputs = document.querySelectorAll('.batch-q-input');
    if (inputs.length > 0) {
        inputs[inputs.length - 1].focus();
    }
}

function removeBatchQuestion(index) {
    batchQuestions.splice(index, 1);
    saveBatchQuestions();
    renderBatchQuestions();
}

function updateBatchQuestion(index, value) {
    batchQuestions[index] = value.trim();
    saveBatchQuestions();
}

function clearBatchQuestions() {
    if (confirm('Reset to default questions?')) {
        batchQuestions = [...DEFAULT_BATCH_QUESTIONS];
        saveBatchQuestions();
        renderBatchQuestions();
    }
}

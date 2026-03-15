// ===== Loader helpers =====

const LOADER_MESSAGES = [
    'Searching documents\u2026',
    'Reasoning across corpus\u2026',
    'Generating answer\u2026',
    'Analyzing sources\u2026'
];

function showQueryLoader() {
    const btn = document.getElementById('submitBtn');
    const hint = document.getElementById('queryStatusHint');
    btn.classList.add('loading');
    btn.onclick = cancelQuery;

    // Show subtle cycling text below input
    let idx = 0;
    hint.textContent = LOADER_MESSAGES[0];
    hint.classList.remove('hidden');
    loaderTextInterval = setInterval(() => {
        idx = (idx + 1) % LOADER_MESSAGES.length;
        hint.style.opacity = '0';
        setTimeout(() => {
            hint.textContent = LOADER_MESSAGES[idx];
            hint.style.opacity = '0.6';
        }, 200);
    }, 3000);
}

function hideQueryLoader() {
    const btn = document.getElementById('submitBtn');
    const hint = document.getElementById('queryStatusHint');
    btn.classList.remove('loading');
    btn.onclick = submitQuery;
    hint.classList.add('hidden');
    if (loaderTextInterval) {
        clearInterval(loaderTextInterval);
        loaderTextInterval = null;
    }
}

function cancelQuery() {
    if (activeQueryController) {
        activeQueryController.abort();
        activeQueryController = null;
    }
    hideQueryLoader();

    // Show cancellation message in thread
    const thread = document.getElementById('conversationThread');
    const cancelDiv = document.createElement('div');
    cancelDiv.className = 'msg-answer';
    cancelDiv.innerHTML = `
        <div class="msg-answer-header">
            <span class="msg-answer-label" style="color:var(--text-muted)">Stopped</span>
        </div>
        <div class="msg-answer-text" style="color:var(--text-muted); font-style:italic">Generation stopped.</div>
    `;
    thread.appendChild(cancelDiv);
    scrollThread();
}

// ===== Single Query =====

async function submitQuery() {
    const input = document.getElementById('queryInput');
    const question = input.value.trim();
    if (!question) return;

    const thread = document.getElementById('conversationThread');

    // Append user question bubble
    const qDiv = document.createElement('div');
    qDiv.className = 'msg-question';
    qDiv.textContent = question;
    thread.appendChild(qDiv);

    input.value = '';

    // Activate loading state on the send button
    showQueryLoader();
    scrollThread();

    // Create AbortController
    activeQueryController = new AbortController();

    try {
        const res = await fetch(`${API_BASE}/api/query/stream`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ question, provider: currentProvider, model: currentModel, project_id: activeProjectId, conversation_id: activeConversationId }),
            signal: activeQueryController.signal
        });

        if (!res.ok) {
            const errData = await res.json().catch(() => ({ error: 'Unknown error' }));
            throw new Error(errData.error || 'Request failed');
        }

        // DON'T hide loader yet — keep it active through the thinking phase.
        // The bubble is lazily created when the first text token arrives.
        let answerDiv = null;
        let textEl = null;
        const streamState = {
            rawText: '',
            thinkingText: '',
            enhancedQuestion: null,
            finalAnswer: null,
            timeSeconds: 0,
        };

        // Lazily create the answer bubble on first text token
        function ensureBubble() {
            if (answerDiv) return;
            hideQueryLoader();
            const result = createStreamBubble();
            answerDiv = result.answerDiv;
            textEl = result.textEl;
            // Copy already-accumulated state into the new bubble's streamState
            Object.assign(result.streamState, streamState);
            thread.appendChild(answerDiv);
            scrollThread();
        }

        // Throttled markdown re-render during streaming
        let renderTimer = null;

        // Extract only the "answer" content from JSON-structured model output
        function extractAnswerFromStream(text) {
            const trimmed = text.trimStart();
            if (!trimmed.startsWith('{')) return text; // Not JSON, passthrough

            // Look for "answer" : "..." and extract content after it
            const marker = /"answer"\s*:\s*"/;
            const match = marker.exec(trimmed);
            if (!match) return ''; // JSON structure but answer field not yet reached

            const afterMarker = trimmed.slice(match.index + match[0].length);
            // The answer value runs until an unescaped quote — but since
            // streaming is incomplete, we just take everything we have so far
            // and unescape JSON string escapes
            let content = afterMarker;
            // If the JSON is complete, trim the trailing " and remaining JSON
            const closingQuote = findUnescapedQuote(content);
            if (closingQuote >= 0) {
                content = content.slice(0, closingQuote);
            }
            // Unescape JSON string escapes
            content = content.replace(/\\n/g, '\n')
                .replace(/\\"/g, '"')
                .replace(/\\\\/g, '\\')
                .replace(/\\t/g, '\t');
            return content;
        }

        // Find the first unescaped double quote in a string
        function findUnescapedQuote(s) {
            for (let i = 0; i < s.length; i++) {
                if (s[i] === '\\') { i++; continue; } // skip escaped char
                if (s[i] === '"') return i;
            }
            return -1;
        }

        const scheduleRender = () => {
            if (renderTimer) return;
            renderTimer = setTimeout(() => {
                renderTimer = null;
                if (!textEl) return;
                const displayText = extractAnswerFromStream(streamState.rawText);
                if (!displayText) return; // Nothing to show yet
                // Show plain text during streaming — markdown applied only on finalize
                textEl.innerHTML = escapeHtml(displayText).replace(/\n/g, '<br>') + '<span class="stream-cursor"></span>';
                scrollThread();
            }, 150);
        };

        // Read SSE stream
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop();

            for (const line of lines) {
                if (!line.startsWith('data: ')) continue;
                const jsonStr = line.slice(6);

                try {
                    const event = JSON.parse(jsonStr);

                    switch (event.type) {
                        case 'text':
                            ensureBubble();
                            streamState.rawText += event.token;
                            scheduleRender();
                            break;

                        case 'thinking':
                            // Accumulate silently — loader stays active
                            streamState.thinkingText += event.token;
                            break;

                        case 'enhanced_question':
                            streamState.enhancedQuestion = event.enhanced_question;
                            break;

                        case 'done':
                            if (event.final) {
                                streamState.finalAnswer = event.final;
                            }
                            break;

                        case 'complete':
                            streamState.timeSeconds = event.time_seconds;
                            break;

                        case 'error':
                            ensureBubble();
                            textEl.innerHTML = '<span style="color:var(--danger)">Error: ' + escapeHtml(event.error || 'Unknown streaming error') + '</span>';
                            break;
                    }
                } catch (e) {
                    // Skip malformed events
                }
            }
        }

        // Cancel any pending render timer
        if (renderTimer) { clearTimeout(renderTimer); renderTimer = null; }

        // If no text tokens were received at all (edge case), ensure the bubble exists
        ensureBubble();

        // Finalize the answer bubble with full formatting
        finalizeStreamAnswer(answerDiv, streamState, question);

        // Auto-name the conversation
        autoNameConversation(question);
    } catch (e) {
        hideQueryLoader();
        activeQueryController = null;

        if (e.name === 'AbortError') return;

        const errDiv = document.createElement('div');
        errDiv.className = 'msg-answer';
        const isNoKeyError = e.message && e.message.toLowerCase().includes('no api key configured');
        const errHtml = isNoKeyError
            ? `<strong>API key not configured.</strong> Please add your API key in <a href="#" onclick="document.getElementById('settingsDropdown')?.classList.remove('hidden');loadSettings();return false;" style="color:var(--accent)">Settings</a> before querying.`
            : escapeHtml(e.message);
        errDiv.innerHTML = `<div class="msg-answer-header"><span class="msg-answer-label" style="color:var(--danger)">Error</span></div><div class="msg-answer-text" style="color:var(--danger)">${errHtml}</div>`;
        thread.appendChild(errDiv);
        scrollThread();
    }

    activeQueryController = null;
}

function createStreamBubble() {
    const answerDiv = document.createElement('div');
    answerDiv.className = 'msg-answer';
    const modelLabel = currentModel || 'default';

    answerDiv.innerHTML = `
        <div class="msg-answer-header">
            <span class="msg-answer-label">Answer</span>
            <span class="msg-answer-time" data-stream-time>streaming\u2026 \u2022 ${currentProvider} / ${modelLabel}</span>
        </div>
        <div data-stream-enhanced style="display:none"></div>
        <div data-stream-thinking style="display:none"></div>
        <div class="msg-answer-text" data-stream-text></div>
        <div data-stream-footnotes style="display:none"></div>
        <div class="msg-confidence" data-stream-confidence style="display:none"></div>
    `;

    const textEl = answerDiv.querySelector('[data-stream-text]');
    const streamState = {
        rawText: '',
        thinkingText: '',
        enhancedQuestion: null,
        finalAnswer: null,
        timeSeconds: 0,
    };

    return { answerDiv, textEl, streamState };
}

function finalizeStreamAnswer(answerDiv, streamState, originalQuestion) {
    const answer = streamState.finalAnswer;
    const textEl = answerDiv.querySelector('[data-stream-text]');
    const timeEl = answerDiv.querySelector('[data-stream-time]');
    const enhancedEl = answerDiv.querySelector('[data-stream-enhanced]');
    const thinkingEl = answerDiv.querySelector('[data-stream-thinking]');
    const footnotesEl = answerDiv.querySelector('[data-stream-footnotes]');
    const confidenceEl = answerDiv.querySelector('[data-stream-confidence]');

    // Update time display
    const timeSec = streamState.timeSeconds ? streamState.timeSeconds.toFixed(2) + 's' : '';
    const modelLabel = currentModel || 'default';
    timeEl.textContent = `${timeSec} \u2022 ${currentProvider} / ${modelLabel}`;

    // Show enhanced question if rewritten
    if (streamState.enhancedQuestion && originalQuestion && streamState.enhancedQuestion !== originalQuestion) {
        enhancedEl.innerHTML = `<div class="msg-enhanced-query" title="Your question was expanded for better search results">\uD83D\uDD0D Searched as: <em>${escapeHtml(streamState.enhancedQuestion)}</em></div>`;
        enhancedEl.style.display = '';
    }

    // Re-render with markdown
    if (answer) {
        let answerHtml = renderMarkdown(answer.answer || streamState.rawText || '');
        answerHtml = answerHtml.replace(/\[(\d+)\]/g, (match, num) => `<span class="footnote-ref">${num}</span>`);
        textEl.innerHTML = answerHtml;

        // Footnotes
        let footnotes = answer.footnotes || [];
        if (footnotes.length === 0 && answer.documents && answer.documents.length > 0) {
            footnotes = answer.documents.map((doc, i) => ({
                id: i + 1,
                document: doc,
                page: answer.pages && answer.pages[i] ? answer.pages[i] : 0
            }));
        }
        if (footnotes.length > 0) {
            const seen = new Set();
            footnotes = footnotes.filter(fn => {
                const key = `${fn.document}:${fn.page}`;
                if (seen.has(key)) return false;
                seen.add(key);
                return true;
            });
            const isPdf = (name) => name.toLowerCase().endsWith('.pdf');
            footnotesEl.innerHTML = `
                <div class="msg-footnotes">
                    <div class="msg-footnotes-title">Sources</div>
                    ${footnotes.map(fn => {
                const clickable = isPdf(fn.document);
                return `<div class="footnote-item${clickable ? ' clickable' : ''}" ${clickable ? `onclick="openDocViewer('${escapeHtml(activeProjectId)}', '${escapeHtml(fn.document).replace(/'/g, "\\'")}', ${fn.page || 1})"` : ''}>
                            <span class="footnote-num">${fn.id}</span>
                            <span class="footnote-doc">${escapeHtml(fn.document)}</span>
                            ${fn.page ? `<span class="footnote-page">p.${fn.page}</span>` : ''}
                            ${clickable ? '<span class="footnote-view-icon" title="View document">&#128196;</span>' : ''}
                        </div>`;
            }).join('')}
                </div>`;
            footnotesEl.style.display = '';

            // Make inline [N] markers clickable
            answerDiv.querySelectorAll('.footnote-ref').forEach(ref => {
                const num = parseInt(ref.textContent);
                const fn = footnotes.find(f => f.id === num);
                if (fn && isPdf(fn.document)) {
                    ref.classList.add('clickable');
                    ref.title = `View ${fn.document} p.${fn.page || 1}`;
                    ref.onclick = () => openDocViewer(activeProjectId, fn.document, fn.page || 1);
                }
            });
        }

        // Confidence
        const conf = answer.confidence || 0;
        const confColor = conf > 0.8 ? 'var(--success)' : conf > 0.5 ? 'var(--warning)' : 'var(--danger)';
        const confReason = answer.confidence_reason || getDefaultConfidenceReason(conf);
        confidenceEl.innerHTML = `
            <div class="confidence-bar">
                <div class="confidence-fill" style="width:${conf * 100}%; background:${confColor}"></div>
            </div>
            <span class="confidence-text">${(conf * 100).toFixed(0)}%</span>
            <span class="confidence-info">?
                <span class="confidence-tooltip">${escapeHtml(confReason)}</span>
            </span>`;
        confidenceEl.style.display = '';

        // Thinking
        const thinkingContent = answer.thinking || streamState.thinkingText;
        if (thinkingContent) {
            const thinkingTextHtml = escapeHtml(thinkingContent).replace(/\n/g, '<br>');
            thinkingEl.innerHTML = `
                <div class="msg-thinking">
                    <button class="msg-thinking-toggle" onclick="this.parentElement.classList.toggle('open')">
                        <span class="thinking-icon">\uD83E\uDDE0</span>
                        <span class="thinking-label">Show reasoning</span>
                        <span class="thinking-chevron">\u25B6</span>
                    </button>
                    <div class="msg-thinking-content">${thinkingTextHtml}</div>
                </div>`;
            thinkingEl.style.display = '';
        }
    } else {
        // No final answer — render raw text with markdown
        let answerHtml = renderMarkdown(streamState.rawText || '');
        answerHtml = answerHtml.replace(/\[(\d+)\]/g, (match, num) => `<span class="footnote-ref">${num}</span>`);
        textEl.innerHTML = answerHtml;
    }

    scrollThread();
}

function appendAnswer(data, originalQuestion) {
    const thread = document.getElementById('conversationThread');
    const answer = data.answer;
    const modelLabel = currentModel || 'default';
    const timeSec = data.time_seconds ? data.time_seconds.toFixed(2) + 's' : '';

    // Show enhanced query if the backend rewrote the question
    let enhancedHtml = '';
    if (data.enhanced_question && originalQuestion && data.enhanced_question !== originalQuestion) {
        enhancedHtml = `<div class="msg-enhanced-query" title="Your question was expanded for better search results">🔍 Searched as: <em>${escapeHtml(data.enhanced_question)}</em></div>`;
    }

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

    // Deduplicate footnotes by document+page to avoid repeated sources
    if (footnotes.length > 0) {
        const seen = new Set();
        footnotes = footnotes.filter(fn => {
            const key = `${fn.document}:${fn.page}`;
            if (seen.has(key)) return false;
            seen.add(key);
            return true;
        });
    }

    const isPdfFile = (name) => name.toLowerCase().endsWith('.pdf');
    if (footnotes.length > 0) {
        footnotesHtml = `
            <div class="msg-footnotes">
                <div class="msg-footnotes-title">Sources</div>
                ${footnotes.map(fn => {
            const clickable = isPdfFile(fn.document);
            return `<div class="footnote-item${clickable ? ' clickable' : ''}" ${clickable ? `onclick="openDocViewer('${escapeHtml(activeProjectId)}', '${escapeHtml(fn.document).replace(/'/g, "\\'")}', ${fn.page || 1})"` : ''}>
                        <span class="footnote-num">${fn.id}</span>
                        <span class="footnote-doc">${escapeHtml(fn.document)}</span>
                        ${fn.page ? `<span class="footnote-page">p.${fn.page}</span>` : ''}
                        ${clickable ? '<span class="footnote-view-icon" title="View document">&#128196;</span>' : ''}
                    </div>`;
        }).join('')}
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
        ${enhancedHtml}
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

    // Make inline [N] markers clickable for PDF sources
    if (footnotes.length > 0) {
        answerDiv.querySelectorAll('.footnote-ref').forEach(ref => {
            const num = parseInt(ref.textContent);
            const fn = footnotes.find(f => f.id === num);
            if (fn && isPdfFile(fn.document)) {
                ref.classList.add('clickable');
                ref.title = `View ${fn.document} p.${fn.page || 1}`;
                ref.onclick = () => openDocViewer(activeProjectId, fn.document, fn.page || 1);
            }
        });
    }
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

    const batchResults = document.getElementById('batchResults');
    batchResults.classList.add('hidden');
    batchResults.innerHTML = '';
    showQueryLoader();

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

        hideQueryLoader();
        renderBatchResults(data);
    } catch (e) {
        clearInterval(timerInterval);
        hideQueryLoader();
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
        document.getElementById('settingsLLM').value = s.default_llm || 'anthropic';
        const embedEl = document.getElementById('settingsEmbed');
        embedEl.value = s.embed_provider || 'openai';
        embedEl.dataset.original = s.embed_provider || 'openai';
        const embedModelEl = document.getElementById('settingsEmbedModel');
        embedModelEl.value = s.embed_model || '';
        embedModelEl.dataset.original = s.embed_model || '';
        document.getElementById('settingsOpenAIKey').value = '';
        document.getElementById('settingsOpenAIKey').placeholder = s.openai_key ? s.openai_key : 'sk-...';
        document.getElementById('settingsAnthropicKey').value = s.anthropic_key || '';
        document.getElementById('settingsAnthropicKey').placeholder = s.anthropic_key ? s.anthropic_key : 'sk-ant-...';
        document.getElementById('settingsHFKey').value = s.huggingface_key || '';
        document.getElementById('settingsHFKey').placeholder = s.huggingface_key ? s.huggingface_key : 'hf_...';
        // Clear the actual value fields — only show masked placeholder
        document.getElementById('settingsAnthropicKey').value = '';
        document.getElementById('settingsHFKey').value = '';
        // OCR settings
        document.getElementById('settingsOCR').value = s.ocr_provider || '';
        document.getElementById('sarvamKeyGroup').style.display = s.ocr_provider === 'sarvam' ? '' : 'none';
        document.getElementById('tesseractLangGroup').style.display = s.ocr_provider === 'sarvam' ? 'none' : '';
        document.getElementById('settingsSarvamKey').value = '';
        document.getElementById('settingsSarvamKey').placeholder = s.sarvam_key ? s.sarvam_key : 'sarvam_...';
        document.getElementById('settingsTesseractLang').value = s.tesseract_lang || 'eng';
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
        embed_model: document.getElementById('settingsEmbedModel').value.trim(),
        ocr_provider: document.getElementById('settingsOCR').value,
        tesseract_lang: document.getElementById('settingsTesseractLang').value.trim(),
    };

    const newEmbedProvider = body.embed_provider;
    const oldEmbedProvider = document.getElementById('settingsEmbed').dataset.original || 'openai';
    const newEmbedModel = body.embed_model;
    const oldEmbedModel = document.getElementById('settingsEmbedModel').dataset.original || '';
    const embedChanged = (oldEmbedProvider !== newEmbedProvider) || (oldEmbedModel !== newEmbedModel);

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
            // Refresh providers and re-check API key banner
            loadProviders();
            checkApiKeySetup();

            document.getElementById('settingsEmbed').dataset.original = newEmbedProvider;
            document.getElementById('settingsEmbedModel').dataset.original = newEmbedModel;

            if (embedChanged) {
                setTimeout(() => {
                    alert("Embedding configuration changed!\\n\\nYou will need to Reset your projects and re-upload documents for semantic search to work correctly with the new embeddings.");
                }, 100);
            }
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

// ===== API Key Validation =====

async function validateApiKey(provider, inputId, btn) {
    const input = document.getElementById(inputId);
    const key = input.value.trim();

    // Map input IDs to status element IDs
    const statusMap = {
        'settingsOpenAIKey': 'keyStatusOpenAI',
        'settingsAnthropicKey': 'keyStatusAnthropic',
        'settingsHFKey': 'keyStatusHuggingFace',
        'settingsSarvamKey': 'keyStatusSarvam',
    };
    const statusEl = document.getElementById(statusMap[inputId]);

    btn.disabled = true;
    btn.textContent = '...';
    if (statusEl) {
        statusEl.textContent = 'Testing...';
        statusEl.className = 'key-status testing';
    }

    // Build request body — include api_key only if user typed a new one
    const reqBody = { provider };
    if (key) reqBody.api_key = key;

    try {
        const res = await fetch(`${API_BASE}/api/settings/validate`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(reqBody)
        });
        const data = await res.json();

        if (statusEl) {
            if (data.valid) {
                statusEl.textContent = '✓ Valid';
                statusEl.className = 'key-status valid';
            } else {
                statusEl.textContent = '✗ ' + (data.error || 'Invalid');
                statusEl.className = 'key-status invalid';
            }
            // Auto-clear after 8 seconds
            setTimeout(() => {
                statusEl.textContent = '';
                statusEl.className = 'key-status';
            }, 8000);
        }
    } catch (e) {
        if (statusEl) {
            statusEl.textContent = '✗ Error: ' + e.message;
            statusEl.className = 'key-status invalid';
        }
    } finally {
        btn.disabled = false;
        btn.textContent = 'Test';
    }
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
        await refreshProjects();
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
    // Update top mode-btn highlighting (only single and search have mode-btns)
    document.querySelectorAll('.mode-btn').forEach(b => b.classList.remove('active'));
    const topBtn = document.querySelector(`[data-mode="${mode}"]`);
    if (topBtn) topBtn.classList.add('active');

    // Toggle panels
    document.getElementById('singlePanel').classList.toggle('hidden', mode !== 'single');
    document.getElementById('searchPanel').classList.toggle('hidden', mode !== 'search');
    document.getElementById('batchPanel').classList.toggle('hidden', mode !== 'batch');
    document.getElementById('singleResult').classList.add('hidden');
    document.getElementById('batchResults').classList.add('hidden');

    // Highlight sidebar batch tool item
    const batchToolBtn = document.getElementById('batchToolBtn');
    if (batchToolBtn) batchToolBtn.classList.toggle('active', mode === 'batch');

    // Focus search input when entering search mode
    if (mode === 'search') {
        setTimeout(() => document.getElementById('searchInput').focus(), 100);
    }
}

function toggleSidebarTools() {
    const list = document.getElementById('sidebarToolsList');
    const chevron = document.querySelector('.sidebar-tools-chevron');
    list.classList.toggle('hidden');
    if (chevron) chevron.classList.toggle('open');
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

// ===== API Key Setup Wizard =====

async function checkApiKeySetup() {
    try {
        const res = await fetch(`${API_BASE}/api/settings`);
        if (!res.ok) return;
        const s = await res.json();
        const hasLLMKey = !!(s.openai_key || s.anthropic_key || s.huggingface_key);
        const hasEmbedKey = s.embed_provider === 'huggingface' ? !!s.huggingface_key : !!s.openai_key;
        const banner = document.getElementById('apiKeyBanner');
        if (!banner) return;
        if (!hasLLMKey || !hasEmbedKey) {
            banner.classList.remove('hidden');
        } else {
            banner.classList.add('hidden');
        }
    } catch (e) {
        // ignore — don't block app startup
    }
}

function openSetupWizard() {
    document.getElementById('setupWizardOverlay').classList.remove('hidden');
}

function closeSetupWizard() {
    document.getElementById('setupWizardOverlay').classList.add('hidden');
    // Re-check after closing in case keys were saved
    checkApiKeySetup();
    loadProviders();
}

async function wizardSaveKey(provider, inputId, btn) {
    const input = document.getElementById(inputId);
    const key = input.value.trim();
    if (!key) return;

    const statusEl = document.getElementById('wizard' + provider.charAt(0).toUpperCase() + provider.slice(1).replace('face','Face') + 'Status');
    btn.disabled = true;
    btn.textContent = 'Saving...';

    try {
        // Save the key
        const body = {};
        if (provider === 'openai') body.openai_key = key;
        else if (provider === 'anthropic') body.anthropic_key = key;
        else if (provider === 'huggingface') body.huggingface_key = key;

        const saveRes = await fetch(`${API_BASE}/api/settings`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
        });
        if (!saveRes.ok) throw new Error('Save failed');

        // Validate the saved key
        const valRes = await fetch(`${API_BASE}/api/settings/validate`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ provider })
        });
        const val = await valRes.json();
        if (statusEl) {
            if (val.valid) {
                statusEl.textContent = '✓ Key saved and validated';
                statusEl.className = 'setup-key-status ok';
                input.value = '';
                input.placeholder = '••••••••••••' + key.slice(-4);
            } else {
                statusEl.textContent = '⚠ Saved, but validation failed: ' + (val.error || 'check your key');
                statusEl.className = 'setup-key-status err';
            }
        }
        // Immediately re-check if banner should be hidden
        checkApiKeySetup();
    } catch (e) {
        if (statusEl) {
            statusEl.textContent = '✗ Error: ' + e.message;
            statusEl.className = 'setup-key-status err';
        }
    }

    btn.disabled = false;
    btn.textContent = 'Save';
}

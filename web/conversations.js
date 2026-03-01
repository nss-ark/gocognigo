// === GoCognigo — Conversation Management ===

async function loadConversations() {
    if (!activeProjectId) return;
    try {
        const res = await fetch(`${API_BASE}/api/conversations?project_id=${activeProjectId}`);
        const data = await res.json();
        conversations = data.conversations || [];

        if (conversations.length === 0) {
            // Auto-create first conversation
            await createNewConversation();
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
            body: JSON.stringify({ project_id: activeProjectId, name: '' })
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
            body: JSON.stringify({ project_id: activeProjectId, conversation_id: convId })
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
                            thinking: meta.thinking || '',
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
            body: JSON.stringify({ project_id: activeProjectId, conversation_id: convId })
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
            body: JSON.stringify({ project_id: activeProjectId, conversation_id: activeConversationId, name: title })
        });
        const idx = conversations.findIndex(c => c.id === activeConversationId);
        if (idx >= 0) conversations[idx].name = title;
        renderSidebar();
    } catch (e) {
        console.error('Auto-name failed', e);
    }
}

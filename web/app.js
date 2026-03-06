// === GoCognigo — App Init ===
// This file wires up DOM event listeners and starts the app.
// All logic lives in the module files loaded before this one.

// Apply saved theme immediately (before DOM renders)
(function () {
    const saved = localStorage.getItem('theme') || 'dark';
    document.documentElement.setAttribute('data-theme', saved);
})();

function toggleTheme() {
    const html = document.documentElement;
    const current = html.getAttribute('data-theme') || 'dark';
    const next = current === 'dark' ? 'light' : 'dark';
    html.setAttribute('data-theme', next);
    localStorage.setItem('theme', next);
}

document.addEventListener('DOMContentLoaded', async () => {
    // Load stats first (sets currentProvider from server default_llm),
    // then load provider models so dropdown matches the active provider.
    await loadStats();
    loadProviders();
    renderBatchQuestions();

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
    document.getElementById('searchModeBtn').addEventListener('click', () => switchMode('search'));

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
    // Global keyboard shortcuts
    document.addEventListener('keydown', (e) => {
        const tag = (e.target.tagName || '').toLowerCase();
        const isInput = tag === 'input' || tag === 'textarea' || tag === 'select';

        // Ctrl+Enter — send query (works even from textarea)
        if (e.ctrlKey && e.key === 'Enter') {
            e.preventDefault();
            if (currentMode === 'single') submitQuery();
            else if (currentMode === 'batch') runBatch();
            return;
        }

        // Escape — close settings dropdown
        if (e.key === 'Escape') {
            const dd = document.getElementById('settingsDropdown');
            if (!dd.classList.contains('hidden')) {
                dd.classList.add('hidden');
                return;
            }
        }

        // Shortcuts below only fire when not typing in an input
        if (isInput) return;

        // Ctrl+N — new conversation
        if (e.ctrlKey && e.key === 'n') {
            e.preventDefault();
            createNewConversation();
            return;
        }

        // Ctrl+Shift+F — toggle search mode
        if (e.ctrlKey && e.shiftKey && e.key === 'F') {
            e.preventDefault();
            switchMode(currentMode === 'search' ? 'single' : 'search');
            return;
        }

        // / — focus query input (vim-style)
        if (e.key === '/') {
            e.preventDefault();
            if (currentMode === 'search') {
                document.getElementById('searchInput').focus();
            } else {
                document.getElementById('queryInput').focus();
            }
            return;
        }
    });

    // Load projects and initialise
    loadProjects();
});

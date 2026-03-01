// === GoCognigo — App Init ===
// This file wires up DOM event listeners and starts the app.
// All logic lives in the module files loaded before this one.

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
    document.getElementById('batchModeBtn').addEventListener('click', () => switchMode('batch'));

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

    // Load projects and initialise
    loadProjects();
});

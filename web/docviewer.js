// === GoCognigo — Document Viewer ===
// Inline PDF viewer with page navigation. Uses browser-native PDF rendering via iframe.

let docViewerOverlay = null;
let docViewerCurrentFile = null;

function createDocViewerOverlay() {
    if (docViewerOverlay) return docViewerOverlay;

    const overlay = document.createElement('div');
    overlay.className = 'doc-viewer-overlay hidden';
    overlay.id = 'docViewerOverlay';
    overlay.innerHTML = `
        <div class="doc-viewer-backdrop" onclick="closeDocViewer()"></div>
        <div class="doc-viewer-container">
            <div class="doc-viewer-header">
                <div class="doc-viewer-file-info">
                    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"></path>
                        <polyline points="14 2 14 8 20 8"></polyline>
                    </svg>
                    <span class="doc-viewer-filename" id="docViewerFilename">document.pdf</span>
                    <span class="doc-viewer-page-badge" id="docViewerPageBadge"></span>
                </div>
                <div class="doc-viewer-controls">
                    <button class="doc-viewer-nav-btn" onclick="docViewerOpenExternal()" title="Open in new tab">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6"></path>
                            <polyline points="15 3 21 3 21 9"></polyline>
                            <line x1="10" y1="14" x2="21" y2="3"></line>
                        </svg>
                    </button>
                    <button class="doc-viewer-close-btn" onclick="closeDocViewer()" title="Close (Esc)">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <line x1="18" y1="6" x2="6" y2="18"></line>
                            <line x1="6" y1="6" x2="18" y2="18"></line>
                        </svg>
                    </button>
                </div>
            </div>
            <div class="doc-viewer-body">
                <iframe class="doc-viewer-iframe" id="docViewerIframe" frameborder="0"></iframe>
            </div>
        </div>
    `;

    document.body.appendChild(overlay);
    docViewerOverlay = overlay;

    // Close on Escape key
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && !overlay.classList.contains('hidden')) {
            closeDocViewer();
        }
    });

    return overlay;
}

function openDocViewer(projectId, filename, page, quote) {
    if (!projectId || !filename) return;

    // Only support PDF files
    const ext = filename.toLowerCase().split('.').pop();
    if (ext !== 'pdf') {
        alert('Inline viewing is only available for PDF files.');
        return;
    }

    const overlay = createDocViewerOverlay();
    const iframe = document.getElementById('docViewerIframe');
    const filenameEl = document.getElementById('docViewerFilename');
    const pageBadge = document.getElementById('docViewerPageBadge');

    // Build the URL for the PDF
    const pageNum = page && page > 0 ? page : 1;
    let hash = `#page=${pageNum}`;
    if (quote && quote.trim() !== '') {
        // Native browser PDF viewers (Chrome/Edge) support #search="text"
        hash += `&search="${encodeURIComponent(quote.trim())}"`;
    }
    const pdfUrl = `${API_BASE}/api/file/view?project_id=${encodeURIComponent(projectId)}&name=${encodeURIComponent(filename)}${hash}`;

    filenameEl.textContent = filename;
    pageBadge.textContent = page && page > 0 ? `Page ${pageNum}` : '';

    // Store current file info for "open in new tab"
    docViewerCurrentFile = pdfUrl;

    // Set iframe src
    iframe.src = pdfUrl;

    // Show overlay with animation
    overlay.classList.remove('hidden');
    requestAnimationFrame(() => {
        overlay.classList.add('visible');
    });

    // Prevent body scroll while viewer is open
    document.body.style.overflow = 'hidden';
}

function closeDocViewer() {
    if (!docViewerOverlay) return;

    docViewerOverlay.classList.remove('visible');
    // Wait for animation to complete before hiding
    setTimeout(() => {
        docViewerOverlay.classList.add('hidden');
        // Clear iframe to stop loading
        const iframe = document.getElementById('docViewerIframe');
        if (iframe) iframe.src = 'about:blank';
    }, 250);

    document.body.style.overflow = '';
    docViewerCurrentFile = null;
}

function docViewerOpenExternal() {
    if (docViewerCurrentFile) {
        window.open(docViewerCurrentFile, '_blank');
    }
}

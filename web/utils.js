// === GoCognigo — Utility Functions ===

function formatFileSize(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

// Lightweight markdown-to-HTML renderer for LLM answers.
// First escapes HTML (safe), then applies markdown patterns.
function renderMarkdown(text) {
    if (!text) return '';
    let html = escapeHtml(text);

    // Code blocks: ```...```
    html = html.replace(/```([\s\S]*?)```/g, '<pre><code>$1</code></pre>');

    // Inline code: `...`
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Bold: **text** or __text__
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/__(.+?)__/g, '<strong>$1</strong>');

    // Italic: *text* or _text_ (but not inside words)
    html = html.replace(/(?<![\w*])\*([^*]+)\*(?![\w*])/g, '<em>$1</em>');
    html = html.replace(/(?<![\w_])_([^_]+)_(?![\w_])/g, '<em>$1</em>');

    // Headers: ### text, ## text, # text (at line start)
    html = html.replace(/^### (.+)$/gm, '<h4>$1</h4>');
    html = html.replace(/^## (.+)$/gm, '<h3>$1</h3>');
    html = html.replace(/^# (.+)$/gm, '<h2>$1</h2>');

    // Numbered lists: handle items separated by blank lines (LLM style)
    html = html.replace(/^(\d+)\.\s(.+)$/gm, (match, num, text) => {
        return `<ol start="${num}"><li>${text}</li></ol>`;
    });
    // Merge adjacent <ol> elements into a single list
    html = html.replace(/<\/ol>\s*(?:<br\s*\/?>|\n)*\s*<ol start="\d+">/g, '');

    // Bullet lists: lines starting with "- " or "* "
    html = html.replace(/((?:^[\-*]\s.+$\n?)+)/gm, (match) => {
        const items = match.trim().split('\n').map(line => {
            return '<li>' + line.replace(/^[\-*]\s/, '') + '</li>';
        }).join('');
        return '<ul>' + items + '</ul>';
    });

    // Paragraphs: double newlines
    html = html.replace(/\n\n+/g, '</p><p>');
    // Single newlines → <br>
    html = html.replace(/\n/g, '<br>');
    // Wrap in paragraph tags
    html = '<p>' + html + '</p>';
    // Clean up empty paragraphs
    html = html.replace(/<p>\s*<\/p>/g, '');
    // Don't wrap block elements in <p>
    html = html.replace(/<p>(<(?:ol|ul|h[2-4]|pre))/g, '$1');
    html = html.replace(/(<\/(?:ol|ul|h[2-4]|pre)>)<\/p>/g, '$1');

    return html;
}

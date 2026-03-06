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

    // Horizontal rules: --- or *** or ___ on a line by itself
    html = html.replace(/^(?:[-*_]){3,}\s*$/gm, '<hr>');

    // Tables: detect consecutive lines with pipe separators (line-by-line approach)
    html = (function parseTablesInText(text) {
        const lines = text.split('\n');
        const result = [];
        let tableLines = [];

        function isTableRow(line) {
            const trimmed = line.trim();
            // A table row has at least 2 pipe characters and contains text between them
            const pipeCount = (trimmed.match(/\|/g) || []).length;
            return pipeCount >= 2;
        }

        function isSeparatorRow(line) {
            // Separator: |---|---| or |:---:|---:| etc.
            return /^\s*\|[\s:]*[-]{2,}[\s:]*\|/.test(line) || /^\s*[-|:\s]+$/.test(line) && (line.match(/\|/g) || []).length >= 2 && (line.match(/-{2,}/g) || []).length >= 1;
        }

        function flushTable() {
            if (tableLines.length < 2) {
                result.push(...tableLines);
                tableLines = [];
                return;
            }

            // Check if second line is a separator
            const hasSeparator = isSeparatorRow(tableLines[1]);
            let headerRow = null;
            let dataRows = tableLines;

            if (hasSeparator) {
                headerRow = tableLines[0];
                dataRows = tableLines.slice(2);
            }

            let tableHtml = '<table>';
            if (headerRow) {
                const cells = headerRow.split('|').map(c => c.trim()).filter(c => c !== '');
                tableHtml += '<thead><tr>' + cells.map(c => `<th>${c}</th>`).join('') + '</tr></thead>';
            }
            if (dataRows.length > 0) {
                tableHtml += '<tbody>';
                for (const row of dataRows) {
                    const cells = row.split('|').map(c => c.trim()).filter(c => c !== '');
                    if (cells.length > 0) {
                        tableHtml += '<tr>' + cells.map(c => `<td>${c}</td>`).join('') + '</tr>';
                    }
                }
                tableHtml += '</tbody>';
            }
            tableHtml += '</table>';
            result.push(tableHtml);
            tableLines = [];
        }

        for (const line of lines) {
            if (isTableRow(line)) {
                tableLines.push(line);
            } else {
                if (tableLines.length > 0) flushTable();
                result.push(line);
            }
        }
        if (tableLines.length > 0) flushTable();

        return result.join('\n');
    })(html);

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
    html = html.replace(/<p>(<(?:ol|ul|h[2-4]|pre|table|hr))/g, '$1');
    html = html.replace(/(<\/(?:ol|ul|h[2-4]|pre|table)>)<\/p>/g, '$1');
    html = html.replace(/<hr><\/p>/g, '<hr>');

    return html;
}

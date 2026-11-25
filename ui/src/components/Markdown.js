import { jsx as _jsx } from "react/jsx-runtime";
// Minimal markdown-ish renderer: fenced code blocks + inline code +
// paragraphs (no heavy deps; SPEC §20 keeps the bundle lean).
function escapeHtml(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function inline(s) {
    return escapeHtml(s).replace(/`([^`]+)`/g, "<code>$1</code>");
}
export function renderMarkdown(text) {
    const parts = text.split(/```(\w*)\n?([\s\S]*?)```/g);
    let html = "";
    for (let i = 0; i < parts.length; i++) {
        if (i % 3 === 0) {
            html += parts[i]
                .split(/\n{2,}/)
                .map((p) => `<p>${inline(p.trim()).replace(/\n/g, "<br/>")}</p>`)
                .filter((p) => p !== "<p></p>")
                .join("");
        }
        else if (i % 3 === 2) {
            html += `<pre><code>${escapeHtml(parts[i])}</code></pre>`;
        }
    }
    return html;
}
export function Markdown({ text }) {
    return (_jsx("div", { className: "md text-[14px] leading-relaxed", dangerouslySetInnerHTML: { __html: renderMarkdown(text) } }));
}

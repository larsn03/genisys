// Monaco editor pane (SPEC §5.4, P3-14). The whole module is lazy-loaded
// (React.lazy in RightPanel) so the main bundle stays lean; plain
// textarea fallback while loading / on error.

import { useEffect, useRef, useState } from "react";
import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import JsonWorker from "monaco-editor/language/json/json.worker.js?worker";
import TsWorker from "monaco-editor/language/typescript/ts.worker.js?worker";
import { useStore } from "../store";

(self as unknown as { MonacoEnvironment: unknown }).MonacoEnvironment = {
  getWorker: (_: string, label: string) => {
    if (label === "json") return new JsonWorker();
    if (label === "typescript" || label === "javascript") return new TsWorker();
    return new EditorWorker();
  },
};

export function EditorPane() {
  const filePath = useStore((s) => s.filePath);
  const fileContent = useStore((s) => s.fileContent);
  const saveFile = useStore((s) => s.saveFile);
  const [draft, setDraft] = useState("");
  const hostRef = useRef<HTMLDivElement>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => setDraft(fileContent), [fileContent]);

  useEffect(() => {
    if (!filePath || !hostRef.current) return;
    setReady(false);
    const model = monaco.editor.createModel(
      fileContent,
      guessLang(filePath),
      monaco.Uri.parse(`file:///${filePath}`),
    );
    const ed = monaco.editor.create(hostRef.current, {
      model,
      theme: "vs-dark",
      automaticLayout: true,
      fontSize: 12.5,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontFamily: "JetBrains Mono, ui-monospace, monospace",
    });
    ed.onDidChangeModelContent(() => setDraft(ed.getValue()));
    setReady(true);
    return () => {
      ed.dispose();
      model.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filePath]);

  const dirty = draft !== fileContent;

  if (!filePath)
    return <div className="text-faint text-xs p-3">Pick a file from the Files tab.</div>;

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="flex-1 min-h-0 relative">
        <div ref={hostRef} className="absolute inset-0" />
        {!ready && (
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="absolute inset-0 bg-transparent outline-none resize-none p-3 font-mono text-[12.5px] leading-relaxed"
            spellCheck={false}
          />
        )}
      </div>
      <div className="p-2 border-t border-line flex gap-2 items-center">
        <button
          onClick={() => void saveFile(draft)}
          disabled={!dirty}
          className="px-3 py-1.5 rounded-lg bg-accent text-white text-[13px] disabled:opacity-40"
        >
          Save
        </button>
        <span className="font-mono text-[11px] text-faint truncate">{filePath}</span>
        {dirty && <span className="text-warn text-xs ml-auto">modified</span>}
      </div>
    </div>
  );
}

function guessLang(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, string> = {
    ts: "typescript", tsx: "typescript", js: "javascript", jsx: "javascript",
    json: "json", py: "python", go: "go", md: "markdown", yaml: "yaml",
    yml: "yaml", rs: "rust", c: "c", h: "c", cpp: "cpp", html: "html",
    css: "css", sh: "shell", toml: "ini", sql: "sql",
  };
  return map[ext] ?? "plaintext";
}

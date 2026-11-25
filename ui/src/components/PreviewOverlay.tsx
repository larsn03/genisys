// Preview overlay (SPEC §17.2): md/text rendered client-side, images
// native, pdf + office docs served through the server preview route
// (LibreOffice → PDF when available; graceful fallback otherwise).

import { useEffect, useState } from "react";
import { api } from "../api";
import { useStore } from "../store";
import { Markdown } from "./Markdown";

const TEXTY = /\.(md|txt|csv|json|ya?ml|log)$/i;
const IMG = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;
const OFFICE = /\.(pptx|docx|xlsx)$/i;

export function PreviewOverlay() {
  const path = useStore((s) => s.preview);
  const close = useStore((s) => s.closePreview);
  const current = useStore((s) => s.current);
  const [text, setText] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    setText(null);
    setFailed(false);
    if (!path || !current || !TEXTY.test(path)) return;
    void api
      .file(current, path)
      .then(setText)
      .catch(() => setFailed(true));
  }, [path, current]);

  if (!path || !current) return null;
  const url = api.previewURL(current, path);
  const name = path.split("/").pop();

  return (
    <div
      className="fixed inset-0 z-40 bg-black/60 flex items-center justify-center p-8"
      onClick={close}
    >
      <div
        className="bg-panel border border-line rounded-2xl shadow-2xl w-full max-w-4xl h-full max-h-[85vh] flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 px-4 py-3 border-b border-line">
          <span className="font-mono text-[13px] truncate flex-1">{name}</span>
          <a
            href={api.fileURL(current, path)}
            download={name}
            className="text-[12px] text-mute hover:text-ink px-2.5 py-1 rounded-lg border border-line"
          >
            ↓ Download
          </a>
          <button
            onClick={close}
            className="text-faint hover:text-ink px-2 py-1"
            title="Close (Esc)"
          >
            ✕
          </button>
        </div>
        <div className="flex-1 min-h-0 overflow-auto">
          {TEXTY.test(path) ? (
            <div className="p-6">
              {text === null ? (
                <div className="text-faint text-sm">loading…</div>
              ) : (
                <Markdown text={text} />
              )}
            </div>
          ) : IMG.test(path) ? (
            <div className="h-full grid place-items-center p-4">
              <img src={url} alt={name} className="max-w-full max-h-full rounded-lg" />
            </div>
          ) : failed ? (
            <Fallback path={path} url={url} />
          ) : (
            <OfficeOrPdf path={path} url={url} onFail={() => setFailed(true)} />
          )}
        </div>
      </div>
    </div>
  );
}

// OfficeOrPdf tries the preview route in an iframe; a 415/404 response
// (no LibreOffice) swaps to the fallback card.
function OfficeOrPdf({ path, url, onFail }: { path: string; url: string; onFail: () => void }) {
  const [state, setState] = useState<"checking" | "ok">("checking");
  useEffect(() => {
    let dead = false;
    void fetch(url, { method: "HEAD" })
      .then((r) => {
        if (dead) return;
        if (r.status === 415 || r.status === 404) onFail();
        else setState("ok");
      })
      .catch(() => !dead && onFail());
    return () => {
      dead = true;
    };
  }, [url, onFail]);
  if (state === "checking")
    return (
      <div className="h-full grid place-items-center text-faint text-sm">
        {OFFICE.test(path) ? "converting to preview…" : "loading…"}
      </div>
    );
  return <iframe title={path} src={url} className="w-full h-full bg-white" />;
}

function Fallback({ path, url }: { path: string; url: string }) {
  return (
    <div className="h-full grid place-items-center">
      <div className="text-center text-mute">
        <div className="text-2xl mb-2">🗎</div>
        <div className="text-sm">No inline preview for {path.split(".").pop()} on this system.</div>
        <a
          href={url.replace("/preview?", "/file?")}
          download
          className="inline-block mt-3 px-3 py-1.5 rounded-lg bg-accent text-white text-[13px]"
        >
          Open externally
        </a>
      </div>
    </div>
  );
}

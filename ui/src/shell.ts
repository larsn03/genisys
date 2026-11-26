// Discovery of the native window shell (desktop.go's bound service).
// The window is frameless; the SPA draws its own titlebar and calls
// back into Go through Wails bindings. In a plain browser there is no
// `window.go`, so findShell() returns null and the app renders without
// window chrome. We feature-scan instead of hardcoding the binding
// path (window.go.<pkg>.<Service>) so Go-side renames can't break us.

export interface ShellAPI {
  Minimize(): unknown;
  ToggleMaximize(): unknown;
  IsMaximized(): unknown;
  Close(): unknown;
}

// v3 bindings may be sync or promise-wrapped; normalise.
export function shellCall(r: unknown): Promise<unknown> {
  return Promise.resolve(r);
}

export function findShell(): ShellAPI | null {
  const go = (window as unknown as { go?: Record<string, Record<string, unknown>> })
    .go;
  if (!go) return null;
  for (const pkg of Object.values(go)) {
    for (const svc of Object.values(pkg ?? {})) {
      const s = svc as ShellAPI;
      if (
        typeof s?.Minimize === "function" &&
        typeof s?.ToggleMaximize === "function" &&
        typeof s?.Close === "function"
      )
        return s;
    }
  }
  return null;
}

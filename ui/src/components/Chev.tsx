// Font-independent chevron glyph (unicode brackets fall back badly in
// WebKitGTK's default font stack).
export function Chev({ dir }: { dir: "left" | "right" }) {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 16 16"
      fill="none"
      className="inline-block align-middle"
      style={{ transform: dir === "right" ? "rotate(180deg)" : undefined }}
    >
      <path
        d="M10 3.5 5.5 8l4.5 4.5"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

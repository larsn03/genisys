"""pdf_simple — minimal pure-Python PDF writer (paperworks, stdlib only).

Enough of ISO 32000 for clean, text-first one-to-many page documents:
Helvetica text, one title line + bullet lines per page. This is the
sandbox-side answer to "make me a PDF" without reportlab (no C
extensions in the WASM guest).
"""


def _esc(s):
    return (
        s.replace("\\", r"\\")
        .replace("(", r"\(")
        .replace(")", r"\)")
        .replace("\r", " ")
        .replace("\n", " ")
    )


def _wrap(text, width=88):
    words, lines, cur = text.split(), [], ""
    for w in words:
        if cur and len(cur) + 1 + len(w) > width:
            lines.append(cur)
            cur = w
        else:
            cur = w if not cur else cur + " " + w
    if cur:
        lines.append(cur)
    return lines


def write_pdf(path, title, sections):
    """sections: list of (heading, [lines]) or plain strings (bullets)."""
    pages = []
    # first page carries the title
    cur = [("F1", 20, 720, _esc(title))]
    height = 660
    for sec in sections:
        if isinstance(sec, str):
            sec = (None, [sec])
        head, lines = sec
        if head:
            if height < 60:
                pages.append(cur)
                cur, height = [], 740
            cur.append(("F2", 13, height, _esc(head)))
            height -= 22
        for ln in lines:
            for w in _wrap(ln):
                if height < 40:
                    pages.append(cur)
                    cur, height = [], 740
                cur.append(("F1", 11, height, "\\225 " + _esc(w)))
                height -= 16
        height -= 8
    if cur:
        pages.append(cur)

    # objects: 1 catalog, 2 pages, 3 font F1, 4 font F2, then per page:
    # page obj + content stream
    objs = []  # (num, bytes)
    page_nums = list(range(5, 5 + 2 * len(pages), 2))
    kids = " ".join(f"{n} 0 R" for n in page_nums)
    objs.append((1, b"<< /Type /Catalog /Pages 2 0 R >>"))
    objs.append((2, f"<< /Type /Pages /Kids [{kids}] /Count {len(pages)} >>".encode()))
    objs.append((3, b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"))
    objs.append((4, b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>"))
    for i, page in enumerate(pages):
        pnum, cnum = page_nums[i], page_nums[i] + 1
        objs.append(
            (
                pnum,
                f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                f"/Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents {cnum} 0 R >>".encode(),
            )
        )
        ops = ["BT"]
        for font, size, y, text in page:
            ops.append(f"/{font} {size} Tf 1 0 0 1 72 {y} Tm ({text}) Tj")
        ops.append("ET")
        stream = "\n".join(ops).encode()
        objs.append(
            (cnum, b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream")
        )

    objs.sort(key=lambda t: t[0])
    out = bytearray(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
    offsets = {}
    for num, body in objs:
        offsets[num] = len(out)
        out += f"{num} 0 obj\n".encode() + body + b"\nendobj\n"
    xref_at = len(out)
    n = max(offsets) + 1
    out += f"xref\n0 {n}\n".encode()
    out += b"0000000000 65535 f \n"
    for i in range(1, n):
        out += f"{offsets.get(i, 0):010d} 00000 n \n".encode()
    out += (
        f"trailer\n<< /Size {n} /Root 1 0 R >>\nstartxref\n{xref_at}\n%%EOF\n".encode()
    )
    with open(path, "wb") as f:
        f.write(out)
    return path

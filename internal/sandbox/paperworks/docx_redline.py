"""docx_redline — tracked-change edits of .docx without lxml (SPEC §17.1).

redline_docx(src, dst, edits=[{"old": "...", "new": "..."}]) copies the
package, rewriting word/document.xml so each matched run becomes a
w:del (old text as w:delText) followed by a w:ins (new text) — Word and
LibreOffice show them as proper tracked changes attributed to Genisys.
"""
import copy
import datetime
import re
import zipfile
import xml.etree.ElementTree as ET


def _zip_mode():
    """ZIP_DEFLATED when zlib exists, STORED otherwise (WASM guest)."""
    try:
        zipfile.ZipFile(_os_devnull(), "w", zipfile.ZIP_DEFLATED).close()
        return zipfile.ZIP_DEFLATED
    except RuntimeError:
        return zipfile.ZIP_STORED


def _os_devnull():
    import os, tempfile
    fd, p = tempfile.mkstemp()
    os.close(fd)
    return p


W = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
ET.register_namespace("w", W)
_AUTHOR, _DATE = "Genisys", datetime.datetime.now().datetime if hasattr(datetime.datetime, "datetime") else "2026-01-01T00:00:00Z"


def _q(tag):
    return f"{{{W}}}{tag}"


def redline_docx(src, dst, edits, author=_AUTHOR):
    """edits: list of {"old": str, "new": str}. Returns applied count."""
    edits = [e for e in edits if e.get("old")]
    applied = 0
    rid = 1000
    with zipfile.ZipFile(src) as zin:
        names = zin.namelist()
        doc = zin.read("word/document.xml")
        others = {n: zin.read(n) for n in names if n != "word/document.xml"}

    root = ET.fromstring(doc)
    body = root.find(_q("body"))

    def run_text(r):
        return "".join(t.text or "" for t in r.findall(_q("t")))

    def make_run(text, kind):
        r = ET.Element(_q("r" if kind != "del" else "r"))
        t = ET.Element(_q("delText" if kind == "del" else "t"))
        t.text = text
        r.append(t)
        return r

    def wrap(child, kind, new_text, old_text):
        nonlocal rid
        rid += 1
        tag = _q("ins") if kind == "ins" else _q("del")
        w = ET.Element(tag, {
            _q("id"): str(rid), _q("author"): author, _q("date"): _DATE,
        })
        w.append(make_run(new_text if kind == "ins" else old_text, kind))
        return w

    for old_new in edits:
        old, new = old_new["old"], old_new.get("new", "")
        for para in root.iter(_q("p")):
            for r in list(para.findall(_q("r"))):
                txt = run_text(r)
                if old not in txt:
                    continue
                idx = txt.find(old)
                before, after = txt[:idx], txt[idx + len(old):]
                pos = list(para).index(r)
                para.remove(r)
                nodes = []
                if before:
                    nr = copy.deepcopy(r)
                    for t in nr.findall(_q("t")):
                        t.text = ""
                    nr.find(_q("t")).text = before
                    nodes.append(nr)
                nodes.append(wrap(r, "del", None, old))
                if new:
                    nodes.append(wrap(r, "ins", new, None))
                if after:
                    nr = copy.deepcopy(r)
                    for t in nr.findall(_q("t")):
                        t.text = ""
                    nr.find(_q("t")).text = after
                    nodes.append(nr)
                for i, nd in enumerate(nodes):
                    para.insert(pos + i, nd)
                applied += 1
                break  # one replacement per paragraph per edit
        if applied:
            break  # first matching edit wins per pass; keep semantics simple

    doc_out = ET.tostring(root, encoding="UTF-8", xml_declaration=True)
    with zipfile.ZipFile(dst, "w", _zip_mode()) as zout:
        zout.writestr("word/document.xml", doc_out)
        for n, data in others.items():
            zout.writestr(n, data)
    return applied


def read_docx_text(path):
    """Plain text of a docx (one paragraph per line) — for load_file_attachment."""
    with zipfile.ZipFile(path) as z:
        root = ET.fromstring(z.read("word/document.xml"))
    lines = []
    for p in root.iter(_q("p")):
        lines.append("".join(t.text or "" for t in p.iter(_q("t"))))
    return "\n".join(lines)

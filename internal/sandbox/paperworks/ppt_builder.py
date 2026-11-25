"""ppt_builder — minimal, dependency-free .pptx construction (SPEC §17.1).

The Genisys sandbox guest runs CPython-WASI without C extensions, so
python-pptx/lxml are unavailable. paperworks writes Office Open XML
directly with zipfile + string templates — valid files that PowerPoint,
Keynote, LibreOffice and python-pptx can all open.

    from ppt_builder import Deck
    d = Deck(title="Q3 Review")
    d.add_title("Q3 Review", "Platform team")
    d.add_bullets("Wins", ["shipped P3", "zero regressions"])
    d.add_assumptions(["endpoint stays up"])   # required by SPEC M3 AC
    d.save("/outputs/deck.pptx")
"""
import zipfile
from xml.sax.saxutils import escape


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


_NS = 'xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"'
_EMU_W, _EMU_H = 12192000, 6858000  # 16:9


def _txbody(paras, wrap=True):
    """paras: list of (text, level, bold, size_pt)."""
    out = []
    for text, lvl, bold, size in paras:
        b = ' b="1"' if bold else ""
        out.append(
            f'<a:p><a:pPr marL="{int(lvl*288000)}" lvl="{lvl}" indent="0"/>'
            f'<a:r><a:rPr lang="en-US" sz="{int(size*100)}"{b} dirty="0"/>'
            f'<a:t>{escape(text)}</a:t></a:r></a:p>'
        )
    return ('<a:txBody><a:bodyPr wrap="square" anchor="t"/><a:lstStyle/>'
            + "".join(out) + "</a:txBody>")


def _shape(shape_id, name, x, y, cx, cy, paras):
    return (
        f'<p:sp><p:nvSpPr><p:cNvPr id="{shape_id}" name="{escape(name)}"/>'
        f'<p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>'
        f'<a:xfrm><a:off x="{x}" y="{y}"/><a:ext cx="{cx}" cy="{cy}"/></a:xfrm>'
        f'<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>'
        + _txbody(paras) + "</p:sp>"
    )


def _slide_xml(shapes):
    return (
        f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
        f'<p:sld {_NS}><p:cSld><p:spTree>'
        f'<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>'
        f'<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/>'
        f'<a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>'
        + "".join(shapes) + "</p:spTree></p:cSld>"
        f'<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>'
    )


class Deck:
    """Theme-aware slide deck; slides are (title, bullets|subtitle) tuples."""

    def __init__(self, title="Presentation", theme="default", author="Genisys"):
        self.title, self.theme, self.author = title, theme, author
        self.slides = []      # list of shape-lists
        self.notes = []       # per-slide speaker notes (or "")
        self._sid = 2

    # -- slide builders (return slide index, 1-based) -------------------
    def add_title(self, title=None, subtitle=""):
        t = title or self.title
        shapes = [
            _shape(self._mkid(), "Title", 685800, 1900000, 10820400, 1500000,
                   [(t, 0, True, 40)]),
            _shape(self._mkid(), "Subtitle", 685800, 3500000, 10820400, 900000,
                   [(subtitle, 0, False, 20)]),
        ]
        return self._push(shapes)

    def add_bullets(self, title, bullets=(), notes=""):
        shapes = [_shape(self._mkid(), "Title", 685800, 300000, 10820400, 1100000,
                         [(title, 0, True, 30)])]
        paras = [(("-  " if lvl == 0 else "  •  ") + b, lvl, False, 18)
                 for b, lvl in _norm(bullets)]
        shapes.append(_shape(self._mkid(), "Body", 685800, 1600000, 10820400, 4800000, paras))
        return self._push(shapes, notes)

    def add_two_col(self, title, left=(), right=(), notes=""):
        shapes = [_shape(self._mkid(), "Title", 685800, 300000, 10820400, 1100000,
                         [(title, 0, True, 30)])]
        shapes.append(_shape(self._mkid(), "Left", 685800, 1600000, 5200000, 4800000,
                             [("•  " + b, 0, False, 16) for b in left]))
        shapes.append(_shape(self._mkid(), "Right", 6300000, 1600000, 5200000, 4800000,
                             [("•  " + b, 0, False, 16) for b in right]))
        return self._push(shapes, notes)

    def add_assumptions(self, assumptions, notes=""):
        """SPEC M3 AC: generated documents state their assumptions."""
        return self.add_bullets("Assumptions", assumptions, notes or "Stated per Genisys policy")

    def add_notes(self, slide_idx, text):
        self.notes[slide_idx - 1] = text

    def _mkid(self):
        self._sid += 1
        return self._sid

    def _push(self, shapes, notes=""):
        self.slides.append(shapes)
        self.notes.append(notes)
        return len(self.slides)

    # -- save ------------------------------------------------------------
    def save(self, path):
        n = len(self.slides)
        sld_ids = "".join(f'<p:sldId id="{256+i}" r:id="rId{i+2}"/>' for i in range(n))
        pres = (
            f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            f'<p:presentation {_NS}>'
            f'<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>'
            f'<p:sldIdLst>{sld_ids}</p:sldIdLst>'
            f'<p:sldSz cx="{_EMU_W}" cy="{_EMU_H}"/><p:notesSz cx="{_EMU_H}" cy="{_EMU_W}"/>'
            f'<p:defaultTextStyle><a:defPPr><a:defRPr lang="en-US"/></a:defPPr></p:defaultTextStyle>'
            f'</p:presentation>'
        )
        pres_rels = ['<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>']
        for i in range(n):
            pres_rels.append(f'<Relationship Id="rId{i+2}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide{i+1}.xml"/>')
        pres_rels_xml = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                         '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                         + "".join(pres_rels) + "</Relationships>")

        ct = ['<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>',
              '<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>',
              '<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>',
              '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>',
              '<Default Extension="xml" ContentType="application/xml"/>']
        for i in range(n):
            ct.append(f'<Override PartName="/ppt/slides/slide{i+1}.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>')
        content_types = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                         '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
                         + "".join(ct) + "</Types>")

        root_rels = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                     '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                     '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>'
                     "</Relationships>")

        master = (f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                  f'<p:sldMaster {_NS}><p:cSld><p:spTree>'
                  f'<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>'
                  f'<p:grpSpPr/></p:spTree></p:cSld>'
                  f'<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>'
                  f'<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>')
        master_rels = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                       '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                       '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>'
                       "</Relationships>")
        layout = (f'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                  f'<p:sldLayout {_NS} type="blank" preserve="1"><p:cSld name="Blank"><p:spTree>'
                  f'<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>'
                  f'<p:grpSpPr/></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>')
        layout_rels = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                       '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                       '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>'
                       "</Relationships>")
        slide_rels = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                      '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                      '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>'
                      "</Relationships>")

        with zipfile.ZipFile(path, "w", _zip_mode()) as z:
            z.writestr("[Content_Types].xml", content_types)
            z.writestr("_rels/.rels", root_rels)
            z.writestr("ppt/presentation.xml", pres)
            z.writestr("ppt/_rels/presentation.xml.rels", pres_rels_xml)
            z.writestr("ppt/slideMasters/slideMaster1.xml", master)
            z.writestr("ppt/slideMasters/_rels/slideMaster1.xml.rels", master_rels)
            z.writestr("ppt/slideLayouts/slideLayout1.xml", layout)
            z.writestr("ppt/slideLayouts/_rels/slideLayout1.xml.rels", layout_rels)
            for i, shapes in enumerate(self.slides):
                z.writestr(f"ppt/slides/slide{i+1}.xml", _slide_xml(shapes))
                z.writestr(f"ppt/slides/_rels/slide{i+1}.xml.rels", slide_rels)
        return path


def _norm(bullets):
    """Accept str, list of str, or list of (text, level) / dicts."""
    if isinstance(bullets, str):
        return [(bullets, 0)]
    out = []
    for b in bullets:
        if isinstance(b, str):
            out.append((b, 0))
        elif isinstance(b, (tuple, list)) and len(b) == 2:
            out.append((str(b[0]), int(b[1])))
        elif isinstance(b, dict):
            out.append((str(b.get("text", "")), int(b.get("level", 0))))
        else:
            out.append((str(b), 0))
    return out

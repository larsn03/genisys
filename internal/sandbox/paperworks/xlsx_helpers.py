"""xlsx_helpers — read/write simple .xlsx with stdlib only (SPEC §17.1).

write_xlsx(path, sheets)  sheets: {"Sheet": [[cell, ...], ...]}
read_xlsx(path)           -> {"Sheet": [[cell, ...], ...]} (inline/shared
                            strings resolved, values as str/float)
"""
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


S = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"


def _q(t):
    return f"{{{S}}}{t}"


def write_xlsx(path, sheets):
    ct_sheets, wb_sheets, rels = [], [], []
    with zipfile.ZipFile(path, "w", _zip_mode()) as z:
        shared, sst_index = [], {}
        sheet_xmls = {}
        for i, (name, rows) in enumerate(sheets.items(), 1):
            ct_sheets.append(f'<Override PartName="/xl/worksheets/sheet{i}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>')
            wb_sheets.append(f'<sheet name="{name}" sheetId="{i}" r:id="rId{i}"/>')
            rels.append(f'<Relationship Id="rId{i}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet{i}.xml"/>')
            row_xml = []
            for ri, row in enumerate(rows, 1):
                cells = []
                for ci, val in enumerate(row):
                    ref = f"{chr(65 + ci % 26)}{ri}" if ci < 26 else f"{chr(64 + ci // 26)}{chr(65 + ci % 26)}{ri}"
                    if isinstance(val, (int, float)) and not isinstance(val, bool):
                        cells.append(f'<c r="{ref}"><v>{val}</v></c>')
                    else:
                        s = str(val)
                        if s not in sst_index:
                            sst_index[s] = len(shared)
                            shared.append(s)
                        cells.append(f'<c r="{ref}" t="s"><v>{sst_index[s]}</v></c>')
                row_xml.append(f'<row r="{ri}">' + "".join(cells) + "</row>")
            sheet_xmls[i] = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                             f'<worksheet xmlns="{S}"><sheetData>' + "".join(row_xml) + "</sheetData></worksheet>")
        sst = ('<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
               f'<sst xmlns="{S}" count="{len(shared)}" uniqueCount="{len(shared)}">'
               + "".join(f"<si><t>{s}</t></si>" for s in shared) + "</sst>")
        z.writestr("[Content_Types].xml",
                   '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                   '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
                   '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
                   '<Default Extension="xml" ContentType="application/xml"/>'
                   '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
                   '<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>'
                   + "".join(ct_sheets) + "</Types>")
        z.writestr("_rels/.rels",
                   '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                   '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                   '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>')
        z.writestr("xl/workbook.xml",
                   '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                   f'<workbook xmlns="{S}" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
                   "<sheets>" + "".join(wb_sheets) + "</sheets></workbook>")
        z.writestr("xl/_rels/workbook.xml.rels",
                   '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                   '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                   + "".join(rels)
                   + f'<Relationship Id="rId{len(rels)+1}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>')
        z.writestr("xl/sharedStrings.xml", sst)
        for i, xml in sheet_xmls.items():
            z.writestr(f"xl/worksheets/sheet{i}.xml", xml)
    return path


def read_xlsx(path):
    with zipfile.ZipFile(path) as z:
        names = z.namelist()
        sst = []
        if "xl/sharedStrings.xml" in names:
            sroot = ET.fromstring(z.read("xl/sharedStrings.xml"))
            for si in sroot.iter(_q("si")):
                sst.append("".join(t.text or "" for t in si.iter(_q("t"))))
        wroot = ET.fromstring(z.read("xl/workbook.xml"))
        sheet_names = [s.get("name") for s in wroot.iter(_q("sheet"))]
        out = {}
        for i, name in enumerate(sheet_names, 1):
            part = f"xl/worksheets/sheet{i}.xml"
            if part not in names:
                continue
            rows = []
            wroot2 = ET.fromstring(z.read(part))
            for row in wroot2.iter(_q("row")):
                vals = []
                for c in row.iter(_q("c")):
                    v = c.find(_q("v"))
                    txt = v.text if v is not None else ""
                    if c.get("t") == "s" and txt:
                        txt = sst[int(txt)]
                    else:
                        try:
                            txt = float(txt) if "." in (txt or "") else int(txt)
                        except ValueError:
                            pass
                    vals.append(txt)
                rows.append(vals)
            out[name] = rows
        return out

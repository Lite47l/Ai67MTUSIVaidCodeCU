"""Read-only inspection of the supplied case documents and workbooks."""

from collections import Counter
from pathlib import Path
from zipfile import ZipFile
import xml.etree.ElementTree as ET

from openpyxl import load_workbook


ROOT = Path(__file__).resolve().parents[2]
NS = {"w": "http://schemas.openxmlformats.org/wordprocessingml/2006/main"}


def document(path):
    with ZipFile(path) as archive:
        root = ET.fromstring(archive.read("word/document.xml"))
    print(f"\n### {path.name}")
    for paragraph in root.findall(".//w:p", NS):
        line = "".join(node.text or "" for node in paragraph.findall(".//w:t", NS)).strip()
        if line:
            print(line)


def workbook(path):
    book = load_workbook(path, read_only=True, data_only=True)
    print(f"\n### {path.name}")
    for sheet in book:
        print(f"SHEET {sheet.title}: rows={sheet.max_row}, columns={sheet.max_column}")
        rows = list(sheet.values)
        for number, row in enumerate(rows[:8], 1):
            print(f"{number}: {row}")
        if len(rows) > 8:
            print(f"... {len(rows) - 8} more rows")
        for col in range(sheet.max_column):
            values = [row[col] for row in rows[1:] if col < len(row)]
            filled = [value for value in values if value is not None and str(value).strip()]
            print(f"COL {col + 1}: filled={len(filled)}/{len(values)} distinct={len(set(map(str, filled)))}")


if __name__ == "__main__":
    for filename in ("задание.docx", "анкета стажера.docx", "анкета наставника.docx"):
        document(ROOT / filename)
    for filename in ("ответы стажеров.xlsx", "оценки наставников.xlsx"):
        workbook(ROOT / filename)

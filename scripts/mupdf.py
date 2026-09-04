import datetime
import sys
import json
import uuid
import pymupdf
import base64
import argparse
import io
from PIL import Image
import openpyxl

FORMAT_VERSION = "1.0.0-lite"

def sanitize_table_value(value):
    if isinstance(value, datetime.date):
        return value.isoformat()
    if (isinstance(value, str) or
        isinstance(value, int) or
        isinstance(value, float) or
        isinstance(value, bool) or
        value is None):
        return value
    if isinstance(value, list):
        return [sanitize_table_value(v) for v in value]
    return str(value)

def extract_tables_from_excel(filename):
    workbook = openpyxl.load_workbook(filename)
    blocks = []
    for sheet in workbook.sheetnames:
        worksheet = workbook[sheet]
        table = []
        for row in worksheet.iter_rows(values_only=True):
            table.append([sanitize_table_value(value) for value in row])
        skip_rows = 0
        if table:
            for i, row in enumerate(table):
                if all(cell is None for cell in row):
                    skip_rows += 1
                else:
                    break
        blocks.append({
            "id": str(uuid.uuid4()),
            "name": sheet,
            "type": "data",
            "content": {
                "headers": [str(col) for col in table[skip_rows]] if table and len(table) > skip_rows else [],
                "rows": table[skip_rows + 1:] if len(table) > skip_rows + 1 else []
            },
        })
    return {
        "format_version": FORMAT_VERSION,
        "id": str(uuid.uuid4()),
        "title": filename,
        "blocks": blocks
    }

def extract_tables_from_pdf(page):
    tables = []
    words = page.get_text("words")
    words.sort(key=lambda w: (-w[3], w[0]))  # sort by top-to-bottom, then left-to-right

    current_table = []
    current_row = []
    last_y = None

    for word in words:
        x0, y0, x1, y1, text = word[:5]  # Take only the first 5 elements
        if last_y is None or abs(y0 - last_y) > 5:  # New row
            if current_row:
                current_table.append(current_row)
                current_row = []
            last_y = y0
        current_row.append(text)

    if current_row:
        current_table.append(current_row)

    if current_table:
        tables.append(current_table)

    return tables

def get_document_structure(doc):
    blocks = []
    for page in doc:
        # tables = extract_tables_from_pdf(page)

        for block in page.get_text("dict")["blocks"]:
            if block["type"] == 0:  # Text block
                block_lines = []
                for line in block["lines"]:
                    for span in line["spans"]:
                        block_lines.append(span["text"])
                blocks.append({
                    "id": str(uuid.uuid4()),
                    "type": "text",
                    "format": "plain",
                    "content": {
                        "text": " ".join(block_lines)
                    },
                })
                # texts.append(span["text"])
            elif block["type"] == 1:  # Image block
                pix = page.get_pixmap(matrix=pymupdf.Matrix(2, 2), clip=block["bbox"])
                img = Image.frombytes("RGB", [pix.width, pix.height], pix.samples)

                # Convert image to PNG and encode as base64
                buffered = io.BytesIO()
                img.save(buffered, format="PNG")
                img_base64 = base64.b64encode(buffered.getvalue()).decode('utf-8')

                blocks.append({
                    "id": str(uuid.uuid4()),
                    "type": "visual",
                    "content": {
                        "type": "image",
                        "data": img_base64,
                    }
                })

    return {
        "format_version": FORMAT_VERSION,
        "id": str(uuid.uuid4()),
        "title": doc.metadata.get("title", ""),
        "blocks": blocks
    }

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Parse a PDF file using OpenParse and OpenAI.")
    parser.add_argument("-i", "--input", help="Path to the PDF file to be parsed")
    parser.add_argument("-o", "--output", help="Path to save the output JSON file (optional)")
    args = parser.parse_args()

    input_file = args.input
    output_file = args.output

    if not input_file:
        print("Input file path is required.")
        sys.exit(1)
    if not output_file:
        print("Output file path is required.")
        sys.exit(1)

    supported_extensions = ('.xlsx', '.pdf', '.pptx', '.doc', '.docx')

    if input_file.lower().endswith(supported_extensions):
        if input_file.lower().endswith('.xlsx'):
            structure = extract_tables_from_excel(input_file)
        else:
            with pymupdf.open(input_file) as doc:
                structure = get_document_structure(doc)
    else:
        print(f"Unsupported file format: {input_file}")
        print(f"Supported formats are: {', '.join(supported_extensions)}")
        sys.exit(1)

    # Convert to JSON and print
    json_structure = json.dumps(structure, indent=2)

    # Optionally, save to a file
    with open(output_file, "w", encoding="utf-8") as f:
        f.write(json_structure)
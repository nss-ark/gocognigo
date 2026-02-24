import sys
import pdfplumber
import json

def extract(pdf_path):
    try:
        with pdfplumber.open(pdf_path) as pdf:
            pages = []
            for i, page in enumerate(pdf.pages):
                text = page.extract_text()
                if text:
                    pages.append({"page": i + 1, "text": text})
            print(json.dumps({"status": "ok", "pages": pages}))
    except Exception as e:
        print(json.dumps({"status": "error", "error": str(e)}))

if __name__ == "__main__":
    extract(sys.argv[1])

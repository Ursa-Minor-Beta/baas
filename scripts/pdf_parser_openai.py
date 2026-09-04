import os
import argparse
import json
from openparse import processing, DocumentParser

def main():
    # Set up argument parser
    parser = argparse.ArgumentParser(description="Parse a PDF file using OpenParse and OpenAI.")
    parser.add_argument("-i", "--input", help="Path to the PDF file to be parsed")
    parser.add_argument("-o", "--output", help="Path to save the output JSON file (optional)")
    args = parser.parse_args()

    semantic_pipeline = processing.SemanticIngestionPipeline(
        openai_api_key=os.environ.get("OPENAI_TOKEN", ""),
        model=os.environ.get("PDF_PARSER_MODEL", "text-embedding-3-large"),
        min_tokens=int(os.environ.get("OPENAI_MIN_TOKENS", 64)),
        max_tokens=int(os.environ.get("OPENAI_MAX_TOKENS", 1024)),
    )
    parser = DocumentParser(
        processing_pipeline=semantic_pipeline,
    )
    print("Trying to parse the PDF file: ", args.input)
    parsed_content = parser.parse(args.input)

    json_output = parsed_content.model_dump_json()

    if args.output:
        with open(args.output, 'w') as f:
            f.write(json_output)
        print(f"JSON output saved to {args.output}")
    else:
        print(json_output)

if __name__ == "__main__":
    main()
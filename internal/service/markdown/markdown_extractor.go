package markdown

import (
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

func ExtractMarkdownFromParsedPdf(parsedPdf dto.ParsedPDF) string {
	output := ""

	for _, block := range parsedPdf.Blocks {
		content, ok := block.Content.(map[string]any)
		if !ok {
			continue
		}
		if block.Name != "" {
			output += "### " + block.Name + "\n\n"
		}
		switch block.Type {
		case "text":
			if text, ok := content["text"].(string); ok {
				output += text + "\n\n"
			}
		case "visual":
			if visualType, ok := content["type"].(string); ok {
				if visualType == "image" {
					if data, ok := content["data"].(string); ok {
						output += "![](data:image/png;base64," + data + ")\n\n"
					}
				}
			}
		case "data":
			if headers, ok := content["headers"].([]any); ok {
				output += "|"
				for _, header := range headers {
					if headerStr, ok := header.(string); ok {
						output += " " + headerStr + " |"
					} else {
						output += "  |"
					}
				}
				output += "\n|"
				for range headers {
					output += " --- |"
				}
				output += "\n"
			}
			if rows, ok := content["rows"].([]any); ok {
				for i, row := range rows {
					if cells, ok := row.([]any); ok {
						output += "|"
						for _, cell := range cells {
							if cellStr, ok := cell.(string); ok {
								output += " " + cellStr + " |"
							} else {
								output += "  |"
							}
						}
						output += "\n"
						if i == 0 {
							output += "|"
							for range cells {
								output += " --- |"
							}
							output += "\n"
						}
					}
				}
				output += "\n"
			}
		}
	}
	return output
}

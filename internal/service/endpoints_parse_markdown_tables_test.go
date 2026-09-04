package service

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestConvertHtmlTablesToMarkdown(t *testing.T) {
	RegisterTestingT(t)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Simple HTML table in markdown",
			input: `# Title

Some text before table.

<table>
<tr><th>Name</th><th>Age</th></tr>
<tr><td>Alice</td><td>30</td></tr>
<tr><td>Bob</td><td>25</td></tr>
</table>

Some text after table.`,
			expected: `# Title

Some text before table.

| Name  | Age |
|-------|-----|
| Alice |  30 |
| Bob   |  25 |

Some text after table.`,
		},
		{
			name: "Multiple HTML tables in markdown",
			input: `# Report

## Table 1
<table>
<tr><th>Product</th><th>Price</th></tr>
<tr><td>Widget</td><td>$10</td></tr>
</table>

## Table 2
<table>
<tr><th>Name</th><th>Email</th></tr>
<tr><td>John</td><td>john@example.com</td></tr>
</table>

End of report.`,
			expected: `# Report

## Table 1

| Product | Price |
|---------|-------|
| Widget  | $10   |

## Table 2

| Name |      Email       |
|------|------------------|
| John | john@example.com |

End of report.`,
		},
		{
			name: "HTML table with nested elements (list)",
			input: `<table>
<tr><th>Column 1</th><th>Column 2</th></tr>
<tr><td><ul><li>Item 1</li><li>Item 2</li></ul></td><td>Data</td></tr>
</table>`,
			expected: `
|   Column 1   | Column 2 |
|--------------|----------|
| Item 1Item 2 | Data     |`,
		},
		{
			name: "Uppercase TABLE tags",
			input: `<TABLE>
<TR><TH>Product</TH><TH>Price</TH></TR>
<TR><TD>Coffee</TD><TD>$5</TD></TR>
</TABLE>`,
			expected: `
| Product | Price |
|---------|-------|
| Coffee  | $5    |`,
		},
		{
			name: "Mixed case table tags",
			input: `<TaBlE>
<tr><th>A</th><th>B</th></tr>
<tr><td>1</td><td>2</td></tr>
</TaBlE>`,
			expected: `
| A | B |
|---|---|
| 1 | 2 |`,
		},
		{
			name:     "No tables - plain markdown",
			input:    `# Title\n\nSome **bold** text and *italic* text.\n\n- List item 1\n- List item 2`,
			expected: `# Title\n\nSome **bold** text and *italic* text.\n\n- List item 1\n- List item 2`,
		},
		{
			name:     "Empty document",
			input:    "",
			expected: "",
		},
		{
			name: "Already markdown table (should not change)",
			input: `| Name | Age |
|------|-----|
| Alice | 30 |
| Bob | 25 |`,
			expected: `| Name | Age |
|------|-----|
| Alice | 30 |
| Bob | 25 |`,
		},
		{
			name: "HTML table with markdown table",
			input: `# Report

Markdown table:
| Name | Value |
|------|-------|
| Test | 123   |

HTML table:
<table>
<tr><th>Product</th><th>Price</th></tr>
<tr><td>Widget</td><td>$10</td></tr>
</table>

End.`,
			expected: `# Report

Markdown table:
| Name | Value |
|------|-------|
| Test | 123   |

HTML table:

| Product | Price |
|---------|-------|
| Widget  | $10   |

End.`,
		},
		{
			name: "Table with empty rows at the end",
			input: `<table>
<tr><th>Name</th><th>Age</th></tr>
<tr><td>Alice</td><td>30</td></tr>
<tr><td></td><td></td></tr>
</table>`,
			expected: `
| Name  | Age |
|-------|-----|
| Alice |  30 |`,
		},
		{
			name: "Table with only header row",
			input: `<table>
<tr><th>Column 1</th><th>Column 2</th><th>Column 3</th></tr>
</table>`,
			expected: `
| Column 1 | Column 2 | Column 3 |
|----------|----------|----------|`,
		},
		{
			name: "Complex markdown with HTML table",
			input: `# Main Title

This is a paragraph with **bold** and *italic*.

## Section 1

- Bullet point 1
- Bullet point 2

<table>
<tr><th>ID</th><th>Status</th></tr>
<tr><td>001</td><td>Active</td></tr>
<tr><td>002</td><td>Inactive</td></tr>
</table>

### Subsection

More text with [link](https://example.com).`,
			expected: `# Main Title

This is a paragraph with **bold** and *italic*.

## Section 1

- Bullet point 1
- Bullet point 2

| ID  |  Status  |
|-----|----------|
| 001 | Active   |
| 002 | Inactive |

### Subsection

More text with [link](https://example.com).`,
		},
		{
			name: "Table with colgroup and complex structure",
			input: `<table>
<colgroup><col style="width:50%"><col style="width:50%"></colgroup>
<thead>
<tr><th>Header 1</th><th>Header 2</th></tr>
</thead>
<tbody>
<tr><td>Data 1</td><td>Data 2</td></tr>
</tbody>
</table>`,
			expected: `
| Header 1 | Header 2 |
|----------|----------|
| Data 1   | Data 2   |`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertHtmlTablesToMarkdown(tt.input)
			if result != tt.expected {
				t.Logf("\n=== INPUT ===\n%q\n\n=== EXPECTED ===\n%q\n\n=== ACTUAL ===\n%q\n", tt.input, tt.expected, result)
			}
			Expect(result).To(Equal(tt.expected), "Failed for test: "+tt.name)
		})
	}
}

func TestConvertHtmlTablesToMarkdown_EdgeCases(t *testing.T) {
	RegisterTestingT(t)

	t.Run("Invalid HTML - should return original", func(t *testing.T) {
		input := `<table><tr><th>Broken`
		result := convertHtmlTablesToMarkdown(input)
		// goquery is forgiving, but we should at least not panic
		Expect(result).NotTo(BeEmpty())
	})

	t.Run("Table with no rows - should skip", func(t *testing.T) {
		input := `Before <table></table> After`
		result := convertHtmlTablesToMarkdown(input)
		// Empty table should be removed or left as is
		Expect(result).To(ContainSubstring("Before"))
		Expect(result).To(ContainSubstring("After"))
	})

	t.Run("Nested tables", func(t *testing.T) {
		input := `<table>
<tr><th>Outer</th><th>Table</th></tr>
<tr><td>Cell 1</td><td><table><tr><td>Nested</td></tr></table></td></tr>
</table>`
		result := convertHtmlTablesToMarkdown(input)
		// Should handle nested tables (extract text)
		Expect(result).To(ContainSubstring("Outer"))
		Expect(result).To(ContainSubstring("Nested"))
	})

	t.Run("Table with special characters in cells", func(t *testing.T) {
		input := `<table>
<tr><th>Name</th><th>Description</th></tr>
<tr><td>Test & Co</td><td>Price: $50 < $100</td></tr>
</table>`
		result := convertHtmlTablesToMarkdown(input)
		Expect(result).To(ContainSubstring("Test & Co"))
		Expect(result).To(ContainSubstring("$50"))
	})

	t.Run("Very large table", func(t *testing.T) {
		// Build a table with 100 rows
		input := `<table><tr><th>ID</th><th>Value</th></tr>`
		for i := 1; i <= 100; i++ {
			input += `<tr><td>` + string(rune(i)) + `</td><td>Value` + string(rune(i)) + `</td></tr>`
		}
		input += `</table>`

		result := convertHtmlTablesToMarkdown(input)
		// Should not panic and should contain markdown table structure
		Expect(result).To(ContainSubstring("|"))
		Expect(result).To(ContainSubstring("ID"))
		Expect(result).To(ContainSubstring("Value"))
	})
}
